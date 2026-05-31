package controller

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"
	cloud "github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/service"
)

type Registry interface {
	List() ([]*domain.Instance, error)
	Delete(string) error
	GetAggregatedMetrics() (float32, int, int, error)
}

var createInstanceFn = func(ctx context.Context, cfg *appconfig.Config) (string, error) {
	return cloud.CreateInstance(ctx, cfg)
}

var gracefulTerminateFn = func(ctx context.Context, cfg *appconfig.Config, instanceID, instanceIP, grpcPort string) error {
	return cloud.GracefulTerminate(ctx, cfg, instanceID, instanceIP, grpcPort)
}

type eventRecorder interface {
	RecordEvent(*domain.SystemEvent)
}

type ASGController struct {
	cfg      *appconfig.Config
	registry Registry
	ec2Cli   *ec2.Client
	eventSvc eventRecorder
	logger   *slog.Logger

	// HU-08: Scale limits (read from config)
	minInstances int
	maxInstances int

	// Scaling thresholds and target
	scaleUpThreshold   float64
	scaleDownThreshold float64
	evaluationWindow   int
	targetLoad         float64 // HU-26: target load to stabilize at (midpoint)
	cooldown           time.Duration

	scaleUpStreak   int
	scaleDownStreak int
	lastScaleAction time.Time
}

// NewASGController creates a new ASG controller reading limits from the provided config.
// HU-08: MinInstances and MaxInstances are loaded from cfg instead of being hardcoded.
func NewASGController(ctx context.Context, cfg *appconfig.Config, registry Registry, eventSvc *service.EventService) (*ASGController, error) {
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %v", err)
	}

	cooldown := time.Duration(cfg.CooldownSeconds) * time.Second
	if cooldown <= 0 {
		cooldown = 180 * time.Second
	}
	evalWindow := cfg.EvaluationWindow
	if evalWindow < 1 {
		evalWindow = 3
	}

	return &ASGController{
		cfg:                cfg,
		registry:           registry,
		ec2Cli:             ec2.NewFromConfig(awscfg),
		eventSvc:           eventSvc,
		logger:             slog.Default().With("component", "ControllerASG"),
		minInstances:       cfg.MinInstances,
		maxInstances:       cfg.MaxInstances,
		scaleUpThreshold:   cfg.ScaleUpThreshold,
		scaleDownThreshold: cfg.ScaleDownThreshold,
		evaluationWindow:   evalWindow,
		targetLoad:         50.0, // HU-26: target load to stabilize at (midpoint)
		cooldown:           cooldown,
	}, nil
}

func (c *ASGController) Start(ctx context.Context) {
	c.logger.Info("starting ControllerASG")
	c.reconcile(ctx)

	interval := 15 * time.Second
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("ControllerASG stopped")
			return
		case <-ticker.C:
			c.reconcile(ctx)
		}
	}
}

func (c *ASGController) reconcile(ctx context.Context) {
	// HU-26: Respect cooldown to prevent scaling thrashing
	if !c.lastScaleAction.IsZero() && time.Since(c.lastScaleAction) < c.cooldown {
		remaining := c.cooldown - time.Since(c.lastScaleAction)
		c.logger.Warn("cooldown active", "remaining", remaining.String())
		if c.eventSvc != nil {
			c.eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventASGCooldownActive,
				domain.SeverityWarning,
				fmt.Sprintf("ASG cooldown active, %v remaining", remaining),
				nil,
			))
		}
		return
	}

	// 1. Fetch AWS instances (with expired token retry)
	awsInstances, err := c.getAWSInstances(ctx)
	if err != nil {
		c.logger.Error("failed to read AWS instances", "error", err)
		return
	}

	// 2. Get local registry instances
	localInstances, err := c.registry.List()
	if err != nil {
		c.logger.Error("failed to list local instances", "error", err)
		return
	}

	// 3. Build AWS map for ghost detection
	awsMap := make(map[string]bool, len(awsInstances))
	for _, id := range awsInstances {
		awsMap[id] = true
	}

	// 4. Classify instances and clean ghosts (HU-22)
	var activeInstances []*domain.Instance
	var totalLoad float32

	for _, inst := range localInstances {
		if !awsMap[inst.ID] && inst.ID != "" && !strings.HasPrefix(inst.ID, "local-") {
			c.logger.Info("removing ghost instance from registry", "instance_id", inst.ID)
			_ = c.registry.Delete(inst.ID)
			continue
		}

		if inst.Status == domain.StatusActive && !inst.ExcludedFromAvg() {
			activeInstances = append(activeInstances, inst)
			totalLoad += getInstanceLoad(inst)
		}
	}

	activeCount := len(activeInstances)

	// Wait for all AWS instances to register before making decisions
	if activeCount < len(awsInstances) {
		c.logger.Info("waiting for instance registration",
			"aws_count", len(awsInstances),
			"active_local", activeCount,
		)
		return
	}

	// 5. HU-12: Recovery — if active count falls below minimum, recover immediately
	if activeCount < c.minInstances {
		deficit := c.minInstances - activeCount
		c.logger.Warn("HU-12: active count below minimum, initiating recovery", "active", activeCount, "min", c.minInstances, "deficit", deficit)
		if c.eventSvc != nil {
			c.eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventFailure,
				domain.SeverityWarning,
				fmt.Sprintf("Active instances (%d) below minimum (%d). Recovering %d instance(s).", activeCount, c.minInstances, deficit),
				map[string]string{
					"active": fmt.Sprintf("%d", activeCount),
					"min":    fmt.Sprintf("%d", c.minInstances),
				},
			))
		}
		for i := 0; i < deficit; i++ {
			instanceID, err := createInstanceFn(ctx, c.cfg)
			if err != nil {
				c.logger.Error("HU-12 recovery failed", "error", err)
				break
			}
			c.logger.Info("HU-12 recovery instance launched", "instance_id", instanceID)
		}
		c.lastScaleAction = time.Now()
		return
	}

	// 6. HU-08 / HU-26: Evaluate scaling based on average load
	if activeCount == 0 {
		return
	}

	avgLoad := totalLoad / float32(activeCount)
	c.logger.Info("ASG Status", "active", activeCount, "avg_load", fmt.Sprintf("%.1f%%", avgLoad))

	scaleUpCond := float64(avgLoad) >= c.scaleUpThreshold && activeCount < c.maxInstances
	scaleDownCond := float64(avgLoad) <= c.scaleDownThreshold && activeCount > c.minInstances

	if scaleUpCond {
		c.scaleUpStreak++
		c.scaleDownStreak = 0
		c.logger.Info("scale-up condition met", "streak", c.scaleUpStreak, "window", c.evaluationWindow)
	} else {
		c.scaleUpStreak = 0
	}

	if scaleDownCond {
		c.scaleDownStreak++
		c.scaleUpStreak = 0
		c.logger.Info("scale-down condition met", "streak", c.scaleDownStreak, "window", c.evaluationWindow)
	} else if !scaleUpCond {
		c.scaleDownStreak = 0
	}

	if c.scaleUpStreak >= c.evaluationWindow && scaleUpCond {
		desiredCount := c.calculateDesiredCount(totalLoad)
		toAdd := desiredCount - activeCount
		if toAdd > 0 {
			c.logger.Info("scale up triggered", "avg_load", avgLoad, "desired", desiredCount, "to_add", toAdd)
			c.scaleUp(ctx, toAdd, float64(avgLoad), activeCount)
			c.lastScaleAction = time.Now()
		}
		c.scaleUpStreak = 0
		c.scaleDownStreak = 0
	} else if c.scaleDownStreak >= c.evaluationWindow && scaleDownCond {
		desiredCount := c.calculateDesiredCount(totalLoad)
		toRemove := activeCount - desiredCount
		if toRemove > 0 {
			c.logger.Info("scale down triggered", "avg_load", avgLoad, "desired", desiredCount, "to_remove", toRemove)
			c.scaleDown(ctx, activeInstances, toRemove, float64(avgLoad), activeCount)
			c.lastScaleAction = time.Now()
		}
		c.scaleUpStreak = 0
		c.scaleDownStreak = 0
	}
}

// calculateDesiredCount computes the optimal instance count to stabilize load at targetLoad (50%).
// HU-26: Greedy load-aware calculation.
// HU-08: Result is always clamped to [minInstances, maxInstances].
func (c *ASGController) calculateDesiredCount(totalLoad float32) int {
	if totalLoad <= 0 {
		return c.minInstances
	}
	desired := int(math.Ceil(float64(totalLoad) / float64(c.targetLoad)))
	if desired < c.minInstances {
		desired = c.minInstances
	}
	if desired > c.maxInstances {
		desired = c.maxInstances
	}
	return desired
}

// scaleUp creates new EC2 instances to handle increased load.
// HU-08: Respects MaxInstances limit (enforced by calculateDesiredCount).
func (c *ASGController) scaleUp(ctx context.Context, count int, avgLoad float64, activeCount int) {
	if c.eventSvc != nil {
		c.eventSvc.RecordEvent(domain.NewSystemEvent(
			domain.EventScaleUpTriggered,
			domain.SeverityWarning,
			fmt.Sprintf("Scale up triggered (avgLoad %.1f%%, adding %d)", avgLoad, count),
			map[string]string{
				"avg_load":      fmt.Sprintf("%.1f", avgLoad),
				"threshold":     fmt.Sprintf("%.1f", c.scaleUpThreshold),
				"active_before": fmt.Sprintf("%d", activeCount),
				"max_instances": fmt.Sprintf("%d", c.maxInstances),
				"count_to_add":  fmt.Sprintf("%d", count),
			},
		))
	}

	for i := 0; i < count; i++ {
		instanceID, err := createInstanceFn(ctx, c.cfg)
		if err != nil {
			c.logger.Error("scale up failed", "error", err)
			if c.eventSvc != nil {
				c.eventSvc.RecordEvent(domain.NewSystemEvent(
					domain.EventFailure,
					domain.SeverityCritical,
					fmt.Sprintf("Scale up failed: %v", err),
					map[string]string{"error": err.Error()},
				))
			}
			break
		}

		c.logger.Info("scale up completed", "instance_id", instanceID)
		if c.eventSvc != nil {
			c.eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventScaleUpCompleted,
				domain.SeverityInfo,
				fmt.Sprintf("Instance %s created (%s)", instanceID, c.cfg.EC2Params.InstanceType),
				map[string]string{
					"instance_id":   instanceID,
					"instance_type": c.cfg.EC2Params.InstanceType,
					"avg_load":      fmt.Sprintf("%.1f", avgLoad),
					"active_before": fmt.Sprintf("%d", activeCount),
				},
			))
		}
	}
}

// scaleDown terminates instances using greedy load-aware selection.
// HU-26: Sorts by cpu_load ascending and removes least loaded first — these contribute
// the least work, so removing them causes the smallest disruption to overall throughput.
// HU-08: Never removes below minInstances.
func (c *ASGController) scaleDown(ctx context.Context, activeInstances []*domain.Instance, count int, avgLoad float64, activeCount int) {
	// Sort by load ascending (lowest load = best candidate for removal)
	sort.Slice(activeInstances, func(i, j int) bool {
		return getInstanceLoad(activeInstances[i]) < getInstanceLoad(activeInstances[j])
	})

	// HU-08: Clamp removals to never go below minimum
	maxRemovable := len(activeInstances) - c.minInstances
	if count > maxRemovable {
		count = maxRemovable
	}
	if count <= 0 {
		c.logger.Warn("HU-08: Cannot scale down further, already at minimum", "min", c.minInstances)
		return
	}

	for i := 0; i < count; i++ {
		inst := activeInstances[i]
		instanceID := inst.ID
		grpcPort := inst.Meta["grpc_port"]
		if grpcPort == "" {
			grpcPort = fmt.Sprintf("%d", c.cfg.MonitorCPort)
		}

		if c.eventSvc != nil {
			c.eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventScaleDownTriggered,
				domain.SeverityWarning,
				fmt.Sprintf("Scale down triggered for instance %s (avgLoad %.1f%%)", instanceID, avgLoad),
				map[string]string{
					"avg_load":        fmt.Sprintf("%.1f", avgLoad),
					"threshold":       fmt.Sprintf("%.1f", c.scaleDownThreshold),
					"active_before":   fmt.Sprintf("%d", activeCount),
					"target_instance": instanceID,
					"cpu_load":        fmt.Sprintf("%.1f", getInstanceLoad(inst)),
				},
			))
		}

		err := gracefulTerminateFn(ctx, c.cfg, instanceID, inst.IP, grpcPort)
		if err != nil {
			c.logger.Error("scale down failed", "instance_id", instanceID, "error", err)
			if c.eventSvc != nil {
				c.eventSvc.RecordEvent(domain.NewSystemEvent(
					domain.EventFailure,
					domain.SeverityCritical,
					fmt.Sprintf("Scale down failed for %s: %v", instanceID, err),
					map[string]string{"error": err.Error(), "instance_id": instanceID},
				))
			}
			continue
		}

		c.logger.Info("scale down completed", "instance_id", instanceID)
		if c.eventSvc != nil {
			c.eventSvc.RecordEvent(domain.NewSystemEvent(
				domain.EventScaleDownCompleted,
				domain.SeverityInfo,
				fmt.Sprintf("Instance %s terminated", instanceID),
				map[string]string{
					"instance_id":   instanceID,
					"avg_load":      fmt.Sprintf("%.1f", avgLoad),
					"active_before": fmt.Sprintf("%d", activeCount),
				},
			))
		}

		if err := c.registry.Delete(instanceID); err != nil {
			c.logger.Warn("failed to delete instance from registry", "instance_id", instanceID, "error", err)
		}
	}
}

// projectTagValue returns the project tag to filter. Defaults to "Teleproy2-ASG".
func (c *ASGController) projectTagValue() string {
	if c.cfg.EC2Params.Tags != nil && c.cfg.EC2Params.Tags["Project"] != "" {
		return c.cfg.EC2Params.Tags["Project"]
	}
	return "Teleproy2-ASG"
}

// getAWSInstances queries EC2 for running instances managed by this ASG.
// HU-08: Uses project tag from config dynamically instead of a hardcoded value.
func (c *ASGController) getAWSInstances(ctx context.Context) ([]string, error) {
	var instances []string
	err := cloud.WithExpiredTokenRetry(ctx, func(ctx context.Context) error {
		input := &ec2.DescribeInstancesInput{
			Filters: []types.Filter{
				{
					Name:   aws.String("tag:Project"),
					Values: []string{c.projectTagValue()},
				},
				{
					Name:   aws.String("instance-state-name"),
					Values: []string{"pending", "running"},
				},
			},
		}
		result, err := c.ec2Cli.DescribeInstances(ctx, input)
		if err != nil {
			return err
		}
		instances = nil
		for _, res := range result.Reservations {
			for _, inst := range res.Instances {
				instances = append(instances, *inst.InstanceId)
			}
		}
		return nil
	})
	return instances, err
}

// getInstanceLoad extracts CPU load from instance metadata.
func getInstanceLoad(inst *domain.Instance) float32 {
	if inst.Meta == nil {
		return 0
	}
	if loadStr, ok := inst.Meta["cpu_load"]; ok {
		if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
			return float32(load)
		}
	}
	return 0
}

// HandleDeadInstance terminates an unresponsive instance and replaces it if below minimum.
func (c *ASGController) HandleDeadInstance(ctx context.Context, inst *domain.Instance) error {
	c.logger.Warn("declaring instance dead", "instance_id", inst.ID)
	grpcPort := inst.Meta["grpc_port"]
	if grpcPort == "" {
		grpcPort = fmt.Sprintf("%d", c.cfg.MonitorCPort)
	}
	if err := gracefulTerminateFn(ctx, c.cfg, inst.ID, inst.IP, grpcPort); err != nil {
		c.logger.Error("terminate dead instance failed", "instance_id", inst.ID, "error", err)
	}
	_ = c.registry.Delete(inst.ID)

	_, activeCount, _, err := c.registry.GetAggregatedMetrics()
	if err != nil {
		local, _ := c.registry.List()
		activeCount = 0
		for _, l := range local {
			if l.Status == domain.StatusActive {
				activeCount++
			}
		}
	}
	if activeCount < c.minInstances {
		if _, err := cloud.RecoverIfNeeded(ctx, c.cfg, activeCount); err != nil {
			return err
		}
	}
	return nil
}
