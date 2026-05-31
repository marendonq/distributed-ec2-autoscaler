package simulation

import (
	"hash/fnv"
	"math"
	"math/rand"
	"sync"
	"time"
)

// LoadSimulator generates synthetic CPU load using a sinusoid plus random noise.
type LoadSimulator struct {
	mu sync.RWMutex

	instanceID string
	min        float64
	max        float64
	period     time.Duration
	noiseAmp   float64
	phase      float64
	start      time.Time

	current float64
}

func NewLoadSimulator(instanceID string, min, max float64, periodSec int, noiseAmp float64) *LoadSimulator {
	mid := (min + max) / 2
	amp := (max - min) / 2
	_ = mid
	_ = amp

	phase := phaseFromInstanceID(instanceID)
	return &LoadSimulator{
		instanceID: instanceID,
		min:        min,
		max:        max,
		period:     time.Duration(periodSec) * time.Second,
		noiseAmp:   noiseAmp,
		phase:      phase,
		start:      time.Now(),
		current:    mid,
	}
}

func phaseFromInstanceID(instanceID string) float64 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(instanceID))
	return float64(h.Sum32()%628) / 100.0
}

func (s *LoadSimulator) Tick() float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	elapsed := time.Since(s.start).Seconds()
	periodSec := s.period.Seconds()
	if periodSec <= 0 {
		periodSec = 120
	}
	mid := (s.min + s.max) / 2
	amp := (s.max - s.min) / 2
	base := mid + amp*math.Sin(2*math.Pi*elapsed/periodSec+s.phase)
	noise := (rand.Float64()*2 - 1) * s.noiseAmp
	s.current = clamp(s.min, s.max, base+noise)
	return s.current
}

func (s *LoadSimulator) Current() float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

func clamp(min, max, v float64) float64 {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
