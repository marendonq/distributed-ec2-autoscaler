package cloud

import (
	"context"
	"fmt"
	"log"
	    
	appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"

)


func RecoverIfNeeded(ctx context.Context, appCfg *appconfig.Config, activeCount int) (string, error) {
	if activeCount < appCfg.MinInstances {
		log.Printf("Active instance count (%d) is below minimum (%d). Initiating recovery.", activeCount, appCfg.MinInstances)
		instanceID, err := CreateInstance(ctx, appCfg)
		if err != nil {
			return "", fmt.Errorf("failed to recover instance: %w", err)
		}
		log.Printf("Recovery successful. New instance %s", instanceID)
		return instanceID, nil
	
	}
	return "", nil
}