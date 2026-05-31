package controller

import (
    "context"
    "fmt"
    "log"
    "time"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    "github.com/aws/aws-sdk-go-v2/service/ec2/types"
    appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/service"
)

type Registry interface {
    List() ([]*domain.Instance, error)
    Delete(string) error
    GetAggregatedMetrics() (float32, int, int, error)
}

type ASGController struct {
    cfg      *appconfig.Config
    registry Registry
    ec2Cli   *ec2.Client
    eventSvc *service.EventService

    // Scale limits
    minInstances int
    maxInstances int

    // Policies
    scaleUpThreshold   float32
    scaleDownThreshold float32
    cooldown           time.Duration

    lastScaleAction time.Time
}

func NewASGController(ctx context.Context, cfg *appconfig.Config, registry Registry, eventSvc *service.EventService) (*ASGController, error) {
    // Attempt to load AWS config. Uses LabRole/Environment implicitly from default chain.
    awscfg, err := config.LoadDefaultConfig(ctx, config.WithRegion("us-east-1"))
    if err != nil {
        return nil, fmt.Errorf("unable to load AWS SDK config: %v", err)
    }

    client := ec2.NewFromConfig(awscfg)

    return &ASGController{
        cfg:                cfg,
        registry:           registry,
        ec2Cli:             client,
        eventSvc:           eventSvc,
        minInstances:       2,
        maxInstances:       5,
        scaleUpThreshold:   70.0,
        scaleDownThreshold: 30.0,
        cooldown:           180 * time.Second, // Word doc: 180s cooldown
    }, nil
}

func (c *ASGController) Start(ctx context.Context) {
    log.Println("Starting ControllerASG...")
    ticker := time.NewTicker(15 * time.Second) // Check every 15 seconds
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

    if !c.lastScaleAction.IsZero() && time.Since(c.lastScaleAction) < c.cooldown {
        remaining := c.cooldown - time.Since(c.lastScaleAction)
        log.Printf("Cooldown active, %v remaining. Skipping.", remaining)
        if c.eventSvc != nil {
            c.eventSvc.RecordEvent(domain.NewSystemEvent(
                domain.EventASGCooldownActive,
                fmt.Sprintf("ASG cooldown active, %v remaining", remaining),
                nil,
            ))
        }
        return
    }

    // HU-20: Obtener métricas agregadas del registry
    avgLoad, activeCount, inactiveCount, err := c.registry.GetAggregatedMetrics()
    if err != nil {
        log.Printf("ControllerASG error getting aggregated metrics: %v", err)
        return
    }

    log.Printf("ControllerASG: avgLoad=%.1f%%, active=%d, inactive=%d", avgLoad, activeCount, inactiveCount)

    // 1. Validar instancias reales vs AWS (HU-22)
    awsInstances, err := c.getAWSInstances(ctx)
    if err != nil {
        log.Printf("ControllerASG error reading from AWS: %v", err)
        // If credentials expired, this handles it gracefully.
        return
    }

    localInstances, err := c.registry.List()
    if err != nil {
        log.Printf("ControllerASG error listing local instances: %v", err)
        return
    }

    // Identificar fantasmas en el registro local
    awsMap := make(map[string]bool)
    for _, id := range awsInstances {
        awsMap[id] = true
    }

    for _, inst := range localInstances {
        // HU-22: Si la instancia local NO existe en AWS, eliminarla del registro
        if !awsMap[inst.ID] && inst.ID != "" && !strings.HasPrefix(inst.ID, "local-") {
            log.Printf("ControllerASG Validación: Instancia %s eliminada de AWS. Eliminando del registro local.", inst.ID)
            c.registry.Delete(inst.ID)
            continue
        }
    }

    // HU-20: Si hay menos instancias activas que en AWS, esperar registro
    if activeCount < len(awsInstances) {
        log.Printf("ControllerASG: Detectadas %d instancias en AWS, pero solo %d activas localmente. Esperando registro...", len(awsInstances), activeCount)
        // No tomamos acciones si no todas se han reportado
        return
    }

    // HU-20: Scale up/down decisions basadas en avgLoad real
    if avgLoad >= c.scaleUpThreshold && activeCount < c.maxInstances {
        log.Printf("ControllerASG: Scale up triggered (avgLoad %.1f%% >= threshold %.1f%%)", avgLoad, c.scaleUpThreshold)
        c.scaleUp(ctx)
        c.lastScaleAction = time.Now()
    } else if avgLoad <= c.scaleDownThreshold && activeCount > c.minInstances {
        log.Printf("ControllerASG: Scale down triggered (avgLoad %.1f%% <= threshold %.1f%%)", avgLoad, c.scaleDownThreshold)
        c.scaleDown(ctx, localInstances)
        c.lastScaleAction = time.Now()
    }
}

func (c *ASGController) getAWSInstances(ctx context.Context) ([]string, error) {
    var instances []string
    input := &ec2.DescribeInstancesInput{
        Filters: []types.Filter{
            {
                Name:   aws.String("tag:Project"),
                Values: []string{"Teleproy2-ASG"},
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

func (c *ASGController) scaleUp(ctx context.Context) {
    log.Println("TODO: scaleUp implementation pending (HU-05)")
    instanceID := "i-placeholder-up"
    // HU-11: registrar creacion de instancia en AWS
    if c.eventSvc != nil {
        c.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventInstanceCreated,
            fmt.Sprintf("Instance %s created (%s)", instanceID, c.cfg.EC2Params.InstanceType),
            map[string]string{"instance_id": instanceID, "instance_type": c.cfg.EC2Params.InstanceType},
        ))
    }
}

func (c *ASGController) scaleDown(ctx context.Context, localInstances []*domain.Instance) {
    log.Println("TODO: scaleDown implementation pending (HU-06/HU-14)")
    instanceID := "i-placeholder-down"
    // HU-11: registrar terminacion de instancia en AWS
    if c.eventSvc != nil {
        c.eventSvc.RecordEvent(domain.NewSystemEvent(
            domain.EventInstanceDeleted,
            fmt.Sprintf("Instance %s terminated", instanceID),
            map[string]string{"instance_id": instanceID},
        ))
    }
}
