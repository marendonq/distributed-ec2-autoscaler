package controller

import (
	"context"
	"fmt"
	"log/slog"
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

	minInstances int
	maxInstances int

	scaleUpThreshold   float64
	scaleDownThreshold float64
	evaluationWindow   int
	cooldown           time.Duration

	scaleUpStreak   int
	scaleDownStreak int
	lastScaleAction time.Time
}

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

	avgLoad, activeCount, inactiveCount, err := c.registry.GetAggregatedMetrics()
	if err != nil {
		c.logger.Error("failed to get aggregated metrics", "error", err)
		return
	}

	c.logger.Info("metrics evaluated",
		"avg_load", fmt.Sprintf("%.1f", avgLoad),
		"active", activeCount,
		"inactive", inactiveCount,
		"scale_up_streak", c.scaleUpStreak,
		"scale_down_streak", c.scaleDownStreak,
	)

	awsInstances, err := c.getAWSInstances(ctx)
	if err != nil {
		c.logger.Error("failed to read AWS instances", "error", err)
		return
	}

	localInstances, err := c.registry.List()
	if err != nil {
		c.logger.Error("failed to list local instances", "error", err)
		return
	}

	awsMap := make(map[string]bool, len(awsInstances))
	for _, id := range awsInstances {
		awsMap[id] = true
	}

	for _, inst := range localInstances {
		if !awsMap[inst.ID] && inst.ID != "" && !strings.HasPrefix(inst.ID, "local-") {
			c.logger.Info("removing ghost instance from registry", "instance_id", inst.ID)
			_ = c.registry.Delete(inst.ID)
		}
	}

	if activeCount < len(awsInstances) {
		c.logger.Info("waiting for instance registration",
			"aws_count", len(awsInstances),
			"active_local", activeCount,
		)
		return
	}

	scaleUp := float64(avgLoad) >= c.scaleUpThreshold && activeCount < c.maxInstances
	scaleDown := float64(avgLoad) <= c.scaleDownThreshold && activeCount > c.minInstances

	if scaleUp {
		c.scaleUpStreak++
		c.scaleDownStreak = 0
		c.logger.Info("scale-up condition met", "streak", c.scaleUpStreak, "window", c.evaluationWindow)
	} else {
		c.scaleUpStreak = 0
	}

	if scaleDown {
		c.scaleDownStreak++
		c.scaleUpStreak = 0
		c.logger.Info("scale-down condition met", "streak", c.scaleDownStreak, "window", c.evaluationWindow)
	} else if !scaleUp {
		c.scaleDownStreak = 0
	}

	if c.scaleUpStreak >= c.evaluationWindow && scaleUp {
		c.logger.Info("scale up triggered",
			"avg_load", avgLoad,
			"threshold", c.scaleUpThreshold,
			"streak", c.scaleUpStreak,
		)
		c.scaleUp(ctx)
		c.lastScaleAction = time.Now()
		c.scaleUpStreak = 0
		c.scaleDownStreak = 0
	} else if c.scaleDownStreak >= c.evaluationWindow && scaleDown {
		c.logger.Info("scale down triggered",
			"avg_load", avgLoad,
			"threshold", c.scaleDownThreshold,
			"streak", c.scaleDownStreak,
		)
		c.scaleDown(ctx, localInstances)
		c.lastScaleAction = time.Now()
		c.scaleUpStreak = 0
		c.scaleDownStreak = 0
	}
}

func (c *ASGController) projectTagValue() string {
	if c.cfg.EC2Params.Tags != nil && c.cfg.EC2Params.Tags["Project"] != "" {
		return c.cfg.EC2Params.Tags["Project"]
	}
	return "ASG-Project2"
}

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

func (c *ASGController) scaleUp(ctx context.Context) {
	avgLoad, activeCount, _, _ := c.registry.GetAggregatedMetrics()
	if c.eventSvc != nil {
		c.eventSvc.RecordEvent(domain.NewSystemEvent(
			domain.EventScaleUpTriggered,
			domain.SeverityWarning,
			fmt.Sprintf("Scale up triggered (avgLoad %.1f%%)", avgLoad),
			map[string]string{
				"avg_load":      fmt.Sprintf("%.1f", avgLoad),
				"threshold":     fmt.Sprintf("%.1f", c.scaleUpThreshold),
				"active_before": fmt.Sprintf("%d", activeCount),
				"max_instances": fmt.Sprintf("%d", c.maxInstances),
			},
		))
	}

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
		return
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

func (c *ASGController) scaleDown(ctx context.Context, localInstances []*domain.Instance) {
	avgLoad, activeCount, _, _ := c.registry.GetAggregatedMetrics()
	candidates := filterScaleDownCandidates(localInstances)
	if len(candidates) == 0 {
		c.logger.Warn("scale down: no candidates")
		return
	}

	victim := selectVictimLIFO(candidates)
	instanceID := victim.ID
	grpcPort := victim.Meta["grpc_port"]
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
			},
		))
	}

	err := gracefulTerminateFn(ctx, c.cfg, instanceID, victim.IP, grpcPort)
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
		return
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

func filterScaleDownCandidates(instances []*domain.Instance) []*domain.Instance {
	var candidates []*domain.Instance
	for _, inst := range instances {
		if !strings.HasPrefix(inst.ID, "local-") && inst.Status == domain.StatusActive && !inst.ExcludedFromAvg() {
			candidates = append(candidates, inst)
		}
	}
	return candidates
}

func selectVictimLIFO(candidates []*domain.Instance) *domain.Instance {
	victim := candidates[0]
	for _, inst := range candidates[1:] {
		if inst.CreatedAtUnix() > victim.CreatedAtUnix() {
			victim = inst
		}
	}
	return victim
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

	avgLoad, activeCount, _, _ := c.registry.GetAggregatedMetrics()
	_ = avgLoad
	if activeCount < c.minInstances {
		if _, err := cloud.RecoverIfNeeded(ctx, c.cfg, activeCount); err != nil {
			return err
		}
	}
	return nil
}
