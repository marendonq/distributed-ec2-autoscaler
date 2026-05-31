package controller

import (
	"context"
	"fmt"
	"log"
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
	"github.com/marendonq/distributed-ec2-autoscaler/internal/adapters/cloud"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

type Registry interface {
	List() ([]*domain.Instance, error)
	Delete(string) error
}

type ASGController struct {
	cfg      *appconfig.Config
	registry Registry
	ec2Cli   *ec2.Client

	// HU-08: Scale limits (read from config)
	minInstances int
	maxInstances int

	// Scaling thresholds and target
	scaleUpThreshold   float32
	scaleDownThreshold float32
	targetLoad         float32       // HU-26: target load to stabilize at (midpoint)
	cooldown           time.Duration

	lastScaleAction time.Time
}

// NewASGController creates a new ASG controller reading limits from the provided config.
// HU-08: MinInstances and MaxInstances are loaded from cfg instead of being hardcoded.
func NewASGController(ctx context.Context, cfg *appconfig.Config, registry Registry) (*ASGController, error) {
	awscfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("unable to load AWS SDK config: %v", err)
	}

	client := ec2.NewFromConfig(awscfg)

	return &ASGController{
		cfg:                cfg,
		registry:           registry,
		ec2Cli:             client,
		minInstances:       cfg.MinInstances,
		maxInstances:       cfg.MaxInstances,
		scaleUpThreshold:   70.0,
		scaleDownThreshold: 30.0,
		targetLoad:         50.0, // HU-26: midpoint between 30-70 for stability
		cooldown:           180 * time.Second,
	}, nil
}

func (c *ASGController) Start(ctx context.Context) {
	log.Println("Starting ControllerASG...")
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("ControllerASG stopped.")
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
		log.Printf("Cooldown active, %v remaining. Skipping.", remaining)
		return
	}

	// 1. Validate real instances vs AWS (HU-22)
	awsInstances, err := c.getAWSInstances(ctx)
	if err != nil {
		log.Printf("ControllerASG error reading from AWS: %v", err)
		return
	}

	// 2. Get local registry instances
	localInstances, err := c.registry.List()
	if err != nil {
		log.Printf("ControllerASG error listing local instances: %v", err)
		return
	}

	// 3. Build AWS map for ghost detection
	awsMap := make(map[string]bool)
	for _, id := range awsInstances {
		awsMap[id] = true
	}

	// 4. Classify instances and clean ghosts
	var activeInstances []*domain.Instance
	var totalLoad float32

	for _, inst := range localInstances {
		// HU-22: Remove ghost instances (registered locally but terminated in AWS)
		if !awsMap[inst.ID] && inst.ID != "" && !strings.HasPrefix(inst.ID, "local-") {
			log.Printf("HU-22 Validación: Instancia %s eliminada de AWS. Eliminando del registro local.", inst.ID)
			c.registry.Delete(inst.ID)
			continue
		}

		if inst.Status == domain.StatusActive {
			activeInstances = append(activeInstances, inst)
			totalLoad += getInstanceLoad(inst)
		}
	}

	activeCount := len(activeInstances)

	// Wait for all AWS instances to register before making decisions
	if activeCount < len(awsInstances) {
		log.Printf("ControllerASG: Detectadas %d instancias en AWS, pero solo %d activas localmente. Esperando registro...", len(awsInstances), activeCount)
		return
	}

	// 5. HU-12: Recovery — if active count falls below minimum, recover immediately
	if activeCount < c.minInstances {
		deficit := c.minInstances - activeCount
		log.Printf("HU-12: Instancias activas (%d) por debajo del mínimo (%d). Recuperando %d instancia(s).",
			activeCount, c.minInstances, deficit)
		for i := 0; i < deficit; i++ {
			instanceID, err := cloud.CreateInstance(ctx, c.cfg)
			if err != nil {
				log.Printf("HU-12: Fallo en recuperación: %v", err)
				break
			}
			log.Printf("HU-12: Instancia de recuperación lanzada: %s", instanceID)
		}
		c.lastScaleAction = time.Now()
		return
	}

	// 6. HU-08 / HU-26: Evaluate scaling based on average load
	if activeCount == 0 {
		return
	}

	avgLoad := totalLoad / float32(activeCount)
	log.Printf("ASG Status: %d activas, carga promedio: %.1f%%", activeCount, avgLoad)

	if avgLoad > c.scaleUpThreshold {
		// Scale UP: calculate how many instances needed to bring avg to targetLoad
		desiredCount := c.calculateDesiredCount(totalLoad)
		toAdd := desiredCount - activeCount
		if toAdd > 0 {
			log.Printf("HU-26: Carga promedio %.1f%% > umbral %.1f%%. Escalando UP: %d → %d instancias.",
				avgLoad, c.scaleUpThreshold, activeCount, desiredCount)
			c.scaleUp(ctx, toAdd)
		}
	} else if avgLoad < c.scaleDownThreshold && activeCount > c.minInstances {
		// Scale DOWN: greedy load-aware selection
		desiredCount := c.calculateDesiredCount(totalLoad)
		toRemove := activeCount - desiredCount
		if toRemove > 0 {
			log.Printf("HU-26: Carga promedio %.1f%% < umbral %.1f%%. Escalando DOWN: %d → %d instancias.",
				avgLoad, c.scaleDownThreshold, activeCount, desiredCount)
			c.scaleDown(ctx, activeInstances, toRemove)
		}
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
func (c *ASGController) scaleUp(ctx context.Context, count int) {
	for i := 0; i < count; i++ {
		instanceID, err := cloud.CreateInstance(ctx, c.cfg)
		if err != nil {
			log.Printf("HU-08: Error creando instancia: %v", err)
			break
		}
		log.Printf("HU-08: Scale UP exitoso, nueva instancia: %s", instanceID)
	}
	c.lastScaleAction = time.Now()
}

// scaleDown terminates instances using greedy load-aware selection.
// HU-26: Sorts by cpu_load ascending and removes least loaded first — these contribute
// the least work, so removing them causes the smallest disruption to overall throughput.
// HU-08: Never removes below minInstances.
func (c *ASGController) scaleDown(ctx context.Context, activeInstances []*domain.Instance, count int) {
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
		log.Printf("HU-08: No se puede escalar más abajo, ya en mínimo (%d).", c.minInstances)
		return
	}

	for i := 0; i < count; i++ {
		inst := activeInstances[i]
		log.Printf("HU-26: Eliminando instancia %s (carga: %.1f%%)", inst.ID, getInstanceLoad(inst))
		if err := cloud.GracefulTerminate(ctx, c.cfg, inst.ID, inst.IP); err != nil {
			log.Printf("HU-26: Error terminando instancia %s: %v", inst.ID, err)
			continue
		}
		c.registry.Delete(inst.ID)
	}
	c.lastScaleAction = time.Now()
}

// getAWSInstances queries EC2 for running instances managed by this ASG.
// HU-08: Uses project tag from config dynamically instead of a hardcoded value.
func (c *ASGController) getAWSInstances(ctx context.Context) ([]string, error) {
	projectTag := "Teleproy2-ASG"
	if tag, ok := c.cfg.EC2Params.Tags["Project"]; ok && tag != "" {
		projectTag = tag
	}

	var instances []string
	input := &ec2.DescribeInstancesInput{
		Filters: []types.Filter{
			{
				Name:   aws.String("tag:Project"),
				Values: []string{projectTag},
			},
			{
				Name:   aws.String("instance-state-name"),
				Values: []string{"pending", "running"},
			},
		},
	}

	result, err := c.ec2Cli.DescribeInstances(ctx, input)
	if err != nil {
		return nil, err
	}

	for _, res := range result.Reservations {
		for _, inst := range res.Instances {
			instances = append(instances, *inst.InstanceId)
		}
	}
	return instances, nil
}

// getInstanceLoad extracts CPU load from instance metadata.
// Returns 0 if not available (until metrics collection is implemented by HU-28).
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
