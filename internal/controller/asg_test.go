package controller

import (
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
