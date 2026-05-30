package controller

import (
    "context"
    "fmt"
    "log"
    "strconv"
    "time"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    "github.com/aws/aws-sdk-go-v2/service/ec2/types"
    appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"
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

    // Scale limits
    minInstances int
    maxInstances int

    // Policies
    scaleUpThreshold   float32
    scaleDownThreshold float32
    cooldown           time.Duration

    lastScaleAction time.Time
}

func NewASGController(ctx context.Context, cfg *appconfig.Config, registry Registry) (*ASGController, error) {
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

    activeCount := 0
    var totalLoad float32 = 0
    var activeIDs []string

    for _, inst := range localInstances {
        // HU-22: Si la instancia local NO existe en AWS, eliminarla del registro
        if !awsMap[inst.ID] && inst.ID != "" && !strings.HasPrefix(inst.ID, "local-") {
            log.Printf("HU-22 Validación: Instancia %s eliminada de AWS. Eliminando del registro local.", inst.ID)
            c.registry.Delete(inst.ID)
            continue
        }

        if inst.Status == domain.StatusActive {
            activeCount++
            activeIDs = append(activeIDs, inst.ID)
            if loadStr, ok := inst.Meta["cpu_load"]; ok {
                if load, err := strconv.ParseFloat(loadStr, 32); err == nil {
                    totalLoad += float32(load)
                }
            }
        }
    }

    // Si AWS dice que hay instancias pero el registro local está vacío (ej: reinicio), 
    // asumimos el activeCount basado en AWS hasta que se registren.
    if activeCount < len(awsInstances) {
        log.Printf("ControllerASG: Detectadas %d instancias en AWS, pero solo %d activas localmente. Esperando registro...", len(awsInstances), activeCount)
        // No tomamos acciones si no todas se han reportado
        return
    }

    // TODO: El cálculo de métricas y la decisión de ScaleUp / ScaleDown serán implementados aquí por otro miembro del equipo.
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
}

func (c *ASGController) scaleDown(ctx context.Context, localInstances []*domain.Instance) {
    log.Println("TODO: scaleDown implementation pending (HU-06/HU-14)")
}
