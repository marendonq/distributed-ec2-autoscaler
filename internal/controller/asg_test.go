package controller

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	appconfig "github.com/marendonq/distributed-ec2-autoscaler/config"
	"github.com/marendonq/distributed-ec2-autoscaler/internal/domain"
)

// MockRegistry implements Registry
type MockRegistry struct {
	instances []*domain.Instance
	deleted   []string
}

func (m *MockRegistry) List() ([]*domain.Instance, error) {
	return m.instances, nil
}

func (m *MockRegistry) Delete(id string) error {
	m.deleted = append(m.deleted, id)
	return nil
}

func (m *MockRegistry) GetAggregatedMetrics() (float32, int, int, error) {
	var total float32
	active := 0
	for _, inst := range m.instances {
		if inst.Status == domain.StatusActive {
			active++
			total += getInstanceLoad(inst)
		}
	}
	avg := float32(0)
	if active > 0 {
		avg = total / float32(active)
	}
	return avg, active, 0, nil
}

func TestGetInstanceLoad(t *testing.T) {
	tests := []struct {
		name     string
		instance *domain.Instance
		expected float32
	}{
		{
			name:     "nil meta",
			instance: &domain.Instance{Meta: nil},
			expected: 0,
		},
		{
			name:     "no cpu_load",
			instance: &domain.Instance{Meta: map[string]string{"env": "local"}},
			expected: 0,
		},
		{
			name:     "valid cpu_load",
			instance: &domain.Instance{Meta: map[string]string{"cpu_load": "45.5"}},
			expected: 45.5,
		},
		{
			name:     "invalid cpu_load format",
			instance: &domain.Instance{Meta: map[string]string{"cpu_load": "invalid"}},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getInstanceLoad(tt.instance)
			if got != tt.expected {
				t.Errorf("getInstanceLoad() = %v, expected %v", got, tt.expected)
			}
		})
	}
}

func TestCalculateDesiredCount(t *testing.T) {
	cfg := &appconfig.Config{
		MinInstances: 2,
		MaxInstances: 5,
	}
	ctrl := &ASGController{
		cfg:          cfg,
		minInstances: cfg.MinInstances,
		maxInstances: cfg.MaxInstances,
		targetLoad:   50.0,
	}

	tests := []struct {
		name      string
		totalLoad float32
		expected  int
	}{
		{
			name:      "zero load returns min",
			totalLoad: 0.0,
			expected:  2,
		},
		{
			name:      "negative load returns min",
			totalLoad: -10.0,
			expected:  2,
		},
		{
			name:      "low load returns min",
			totalLoad: 40.0, // 40 / 50 = 0.8 -> ceil is 1 -> clamped to min (2)
			expected:  2,
		},
		{
			name:      "medium load",
			totalLoad: 120.0, // 120 / 50 = 2.4 -> ceil is 3
			expected:  3,
		},
		{
			name:      "high load clamped to max",
			totalLoad: 400.0, // 400 / 50 = 8.0 -> clamped to max (5)
			expected:  5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ctrl.calculateDesiredCount(tt.totalLoad)
			if got != tt.expected {
				t.Errorf("calculateDesiredCount(%v) = %v, expected %v", tt.totalLoad, got, tt.expected)
			}
		})
	}
}

func TestScaleDownGreedySelection(t *testing.T) {
	// Override package-level global functions to mock AWS EC2
	origTerminate := gracefulTerminateFn
	defer func() {
		gracefulTerminateFn = origTerminate
	}()

	var terminated []string
	gracefulTerminateFn = func(ctx context.Context, cfg *appconfig.Config, instanceID, instanceIP, grpcPort string) error {
		terminated = append(terminated, instanceID)
		return nil
	}

	cfg := &appconfig.Config{
		MinInstances: 2,
		MaxInstances: 5,
	}
	mockReg := &MockRegistry{
		instances: []*domain.Instance{
			{ID: "inst-high", IP: "10.0.0.1", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "85.0"}},
			{ID: "inst-mid", IP: "10.0.0.2", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "45.0"}},
			{ID: "inst-low", IP: "10.0.0.3", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "10.0"}},
			{ID: "inst-lowest", IP: "10.0.0.4", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "5.0"}},
		},
	}

	ctrl := &ASGController{
		cfg:                cfg,
		registry:           mockReg,
		minInstances:       cfg.MinInstances,
		maxInstances:       cfg.MaxInstances,
		targetLoad:         50.0,
		scaleDownThreshold: 30.0,
		logger:             slog.Default(),
	}

	// We have 4 active instances.
	// Total load: 85 + 45 + 10 + 5 = 145.0%
	// Average load: 145 / 4 = 36.25% (above 30%, so no automatic scaleDown in reconcile).
	// However, we want to directly test scaleDown logic to verify it terminates the lowest-load instances (greedy selection).
	// Let's say we need to scale down from 4 to 2 instances (so we remove 2 instances).
	// The ones with the lowest loads are "inst-lowest" (5.0) and "inst-low" (10.0).
	ctrl.scaleDown(context.Background(), mockReg.instances, 2, 36.25, 4)

	if len(terminated) != 2 {
		t.Fatalf("expected 2 instances to be terminated, got %d", len(terminated))
	}

	// Because of sorting ascending, the first terminated should be "inst-lowest", then "inst-low"
	if terminated[0] != "inst-lowest" {
		t.Errorf("expected first terminated instance to be 'inst-lowest', got '%s'", terminated[0])
	}
	if terminated[1] != "inst-low" {
		t.Errorf("expected second terminated instance to be 'inst-low', got '%s'", terminated[1])
	}
}

func TestScaleUpPredictive(t *testing.T) {
	origCreate := createInstanceFn
	defer func() {
		createInstanceFn = origCreate
	}()

	var launched []string
	createInstanceFn = func(ctx context.Context, cfg *appconfig.Config) (string, error) {
		id := fmt.Sprintf("launched-%d", len(launched)+1)
		launched = append(launched, id)
		return id, nil
	}

	cfg := &appconfig.Config{
		MinInstances: 2,
		MaxInstances: 5,
	}
	mockReg := &MockRegistry{
		instances: []*domain.Instance{
			{ID: "inst-1", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "85.0"}},
			{ID: "inst-2", Status: domain.StatusActive, Meta: map[string]string{"cpu_load": "75.0"}},
		},
	}

	ctrl := &ASGController{
		cfg:              cfg,
		registry:         mockReg,
		minInstances:     cfg.MinInstances,
		maxInstances:     cfg.MaxInstances,
		targetLoad:       50.0,
		scaleUpThreshold: 70.0,
		logger:           slog.Default(),
	}

	// We have 2 active instances.
	// Total load: 85 + 75 = 160%
	// Average load: 160 / 2 = 80% (above scaleUpThreshold 70%).
	// Desired count: ceil(160 / 50) = 4.
	// Count to add: 4 - 2 = 2.
	ctrl.scaleUp(context.Background(), 2, 80.0, 2)

	if len(launched) != 2 {
		t.Fatalf("expected 2 instances to be launched, got %d", len(launched))
	}
	if launched[0] != "launched-1" || launched[1] != "launched-2" {
		t.Errorf("unexpected launched instance IDs: %v", launched)
	}
}
