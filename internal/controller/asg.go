package controller

import (
    "context"
    "fmt"
    "log"
    "sort"
    "strconv"
    "time"
    "strings"

    "github.com/aws/aws-sdk-go-v2/aws"
    "github.com/aws/aws-sdk-go-v2/config"
    "github.com/aws/aws-sdk-go-v2/service/ec2"
    "github.com/aws/aws-sdk-go-v2/service/ec2/types"
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"

    appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"
    "github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
    pb "github.com/marendonq/distributed-ec2-autoscaler/api/proto/monitor"
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
        activeCount = len(awsInstances)
        // No tomamos acciones si no todas se han reportado
        return
    }

    // Si no hay cooldown activo, evaluar métricas (HU-35)
    if time.Since(c.lastScaleAction) < c.cooldown {
        return
    }

    if activeCount == 0 && c.minInstances > 0 {
        log.Printf("No active instances. Scaling up to reach minInstances (%d)", c.minInstances)
        c.scaleUp(ctx)
        return
    }

    avgLoad := totalLoad / float32(activeCount)
    log.Printf("ControllerASG Status: %d instances, AvgLoad: %.2f", activeCount, avgLoad)

    if avgLoad > c.scaleUpThreshold && activeCount < c.maxInstances {
        log.Printf("AvgLoad (%.2f) > Threshold (%.2f). Scaling UP.", avgLoad, c.scaleUpThreshold)
        c.scaleUp(ctx)
    } else if avgLoad < c.scaleDownThreshold && activeCount > c.minInstances {
        log.Printf("AvgLoad (%.2f) < Threshold (%.2f). Scaling DOWN.", avgLoad, c.scaleDownThreshold)
        c.scaleDown(ctx, localInstances) // Pass localInstances to find LIFO
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
    // Configuración dura-codeada para el proyecto (t2.micro, etc.)
    // Idealmente vendría del config.json
    
    // IP privada de la instancia MonitorS (para user-data)
    monitorS_IP := "172.20.0.10" // Reemplazar en config real

    userData := fmt.Sprintf("#!/bin/bash\necho \"export MONITOR_S_IP=%s\" > /etc/monitor_c.env\nsystemctl restart monitorc\n", monitorS_IP)

    input := &ec2.RunInstancesInput{
        ImageId:      aws.String("ami-0c7217cdde317cfec"), // Reemplazar con AMI base real
        InstanceType: types.InstanceTypeT2Micro,
        MinCount:     aws.Int32(1),
        MaxCount:     aws.Int32(1),
        KeyName:      aws.String("vockey"),
        UserData:     aws.String(userData),
        TagSpecifications: []types.TagSpecification{
            {
                ResourceType: types.ResourceTypeInstance,
                Tags: []types.Tag{
                    {Key: aws.String("Project"), Value: aws.String("Teleproy2-ASG")},
                    {Key: aws.String("Name"), Value: aws.String("AppInstance-AutoScaled")},
                },
            },
        },
    }

    log.Println("Calling AWS EC2 RunInstances...")
    _, err := c.ec2Cli.RunInstances(ctx, input)
    if err != nil {
        log.Printf("Scale UP failed: %v", err)
        return
    }
    
    log.Println("Scale UP successful. Cooldown started.")
    c.lastScaleAction = time.Now()
}

func (c *ASGController) scaleDown(ctx context.Context, localInstances []*domain.Instance) {
    // Seleccionar la más nueva (LIFO) según el Word doc
    sort.Slice(localInstances, func(i, j int) bool {
        return localInstances[i].LastSeen > localInstances[j].LastSeen // Más reciente primero
    })

    var target *domain.Instance
    for _, inst := range localInstances {
        if inst.Status == domain.StatusActive {
            target = inst
            break
        }
    }

    if target == nil {
        return
    }

    log.Printf("Selected instance %s for Scale DOWN", target.ID)

    // Shutdown ordenado vía gRPC
    port := target.Meta["grpc_port"]
    if port == "" {
        port = "50052"
    }
    addr := fmt.Sprintf("%s:%s", target.IP, port)
    
    grpcCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
    defer cancel()
    
    conn, err := grpc.DialContext(grpcCtx, addr, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
    if err == nil {
        client := pb.NewMonitorCServiceClient(conn)
        client.Shutdown(grpcCtx, &pb.ShutdownRequest{})
        conn.Close()
        log.Printf("Sent graceful shutdown to %s", target.ID)
    }

    // Call EC2 Terminate
    input := &ec2.TerminateInstancesInput{
        InstanceIds: []string{target.ID},
    }

    _, err = c.ec2Cli.TerminateInstances(ctx, input)
    if err != nil {
        log.Printf("Scale DOWN failed to terminate in AWS: %v", err)
        return
    }

    c.registry.Delete(target.ID)
    log.Println("Scale DOWN successful. Cooldown started.")
    c.lastScaleAction = time.Now()
}
