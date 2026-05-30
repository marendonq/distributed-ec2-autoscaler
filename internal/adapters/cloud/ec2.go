package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	    
	appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"

)

func CreateInstance(ctx context.Context, appCfg *appconfig.Config) (string, error) {
	sdkcfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(appCfg.Region))
	if err != nil {
		return "", fmt.Errorf("failed to load AWS configuration: %w", err)
	}

	// Create an ec2 client
	client := ec2.NewFromConfig(sdkcfg)

	userDataRaw := "#!/bin/bash\necho \"MONITOR_S_IP=" + appCfg.MonitorSIP + "\" > /etc/monitor_c.env"
	userDataB64 := base64.StdEncoding.EncodeToString([]byte(userDataRaw))


	input := &ec2.RunInstancesInput{
		ImageId:          aws.String(appCfg.EC2Params.AMI),
        InstanceType:     types.InstanceTypeT2Micro,
        KeyName:          aws.String(appCfg.EC2Params.KeyName),
        MinCount:         aws.Int32(1),
        MaxCount:         aws.Int32(1),
		SecurityGroupIds: appCfg.EC2Params.SecurityGroups,
        SubnetId:         aws.String(appCfg.EC2Params.SubnetID),
        UserData:         aws.String(userDataB64),
        TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeInstance,
				Tags: []types.Tag{
					{Key: aws.String("Name"), Value: aws.String("AppInstance-ASG")},
					{Key: aws.String("ManagedBy"), Value: aws.String(appCfg.EC2Params.Tags["ManagedBy"])},
					{Key: aws.String("Project"), Value: aws.String(appCfg.EC2Params.Tags["Project"])},
				},
			},
		},
	}
	result, err := client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to create instance: %w", err)
	}

	instanceID := *result.Instances[0].InstanceId
	log.Printf("Created instance with ID: %s", instanceID)
	return instanceID, nil
}

func TerminateInstance(ctx context.Context, appCfg *appconfig.Config, instanceID string) error {
	sdkcfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(appCfg.Region))
	if err != nil {
		return fmt.Errorf("failed to load AWS configuration: %w", err)
	}
		client := ec2.NewFromConfig(sdkcfg)

	input := &ec2.TerminateInstancesInput{
		InstanceIds: []string{instanceID},
	}
	result, err := client.TerminateInstances(ctx, input)
	if err != nil {
		return fmt.Errorf("failed to terminate instance: %w", err)
	}
	log.Printf("Terminated instance: %s, state: %s", instanceID, result.TerminatingInstances[0].CurrentState.Name)
	return nil
}

func GracefulTerminate(ctx context.Context, appCfg *appconfig.Config, instanceID string, instanceIP string) error {
	log.Printf("Initiating graceful termination for instance %s at %s", instanceID, instanceIP)
	// TODO: call MonitorC.Shutdown gRPC at IP:port
	time.Sleep(5 * time.Second) 
	return TerminateInstance(ctx, appCfg, instanceID)
}