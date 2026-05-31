package tools

import (
	"time"
)

type LoadSimulator struct {
	targetLoad    float32
	currentLoad   float32
	increment     float32
	decrement     float32
	minLoad       float32
	maxLoad       float32
	updateChannel chan float32
	stopChannel   chan struct{}
}

func NewLoadSimulator() *LoadSimulator {
	return &LoadSimulator{
		targetLoad:    20.0,
		currentLoad:   20.0,
		increment:     0.5,
		decrement:     0.3,
		minLoad:       5.0,
		maxLoad:       95.0,
		updateChannel: make(chan float32, 1),
		stopChannel:   make(chan struct{}),
	}
}

func (ls *LoadSimulator) Start() {
	go ls.simulate()
}

func (ls *LoadSimulator) Stop() {
	close(ls.stopChannel)
}

func (ls *LoadSimulator) SetTargetLoad(target float32) {
	if target < ls.minLoad {
		target = ls.minLoad
	}
	if target > ls.maxLoad {
		target = ls.maxLoad
	}
	ls.targetLoad = target
}

func (ls *LoadSimulator) GetLoad() float32 {
	return ls.currentLoad
}

func (ls *LoadSimulator) simulate() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ls.stopChannel:
			return
		case <-ticker.C:
			ls.updateLoad()
		}
	}
}

func (ls *LoadSimulator) updateLoad() {
	if ls.targetLoad > ls.currentLoad {
		ls.currentLoad += ls.increment
		if ls.currentLoad > ls.targetLoad {
			ls.currentLoad = ls.targetLoad
		}
	} else if ls.targetLoad < ls.currentLoad {
		ls.currentLoad -= ls.decrement
		if ls.currentLoad < ls.targetLoad {
			ls.currentLoad = ls.targetLoad
		}
	}

	variation := float32((time.Now().Unix() / 10) % 5) - 2
	ls.currentLoad += variation * 0.01

	if ls.currentLoad < ls.minLoad {
		ls.currentLoad = ls.minLoad
	}
	if ls.currentLoad > ls.maxLoad {
		ls.currentLoad = ls.maxLoad
	}

	select {
	case ls.updateChannel <- ls.currentLoad:
	default:
	}
}
