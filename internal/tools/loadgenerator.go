package tools

import (
	"math"
	"runtime"
	"time"
)

type LoadGenerator struct {
	loadChannel chan float32
	doneChannel chan struct{}
	workers     int
	quit        chan struct{}
}

func NewLoadGenerator(workers int) *LoadGenerator {
	return &LoadGenerator{
		loadChannel: make(chan float32, 1),
		doneChannel: make(chan struct{}),
		workers:     workers,
		quit:        make(chan struct{}),
	}
}

func (lg *LoadGenerator) Start(targetLoad float32) {
	go lg.generateLoad(targetLoad)
}

func (lg *LoadGenerator) Stop() {
	close(lg.quit)
}

func (lg *LoadGenerator) generateLoad(targetLoad float32) {
	cpuCount := runtime.NumCPU()
	if lg.workers > 0 {
		cpuCount = lg.workers
	}

	activeWorkers := int(float32(cpuCount) * targetLoad / 100.0)
	if activeWorkers < 1 && targetLoad > 0 {
		activeWorkers = 1
	}
	if activeWorkers > cpuCount {
		activeWorkers = cpuCount
	}

	quitBusy := make(chan struct{})
	for i := 0; i < activeWorkers; i++ {
		go busyLoop(quitBusy)
	}

	quitIdle := make(chan struct{})
	idleWorkers := cpuCount - activeWorkers
	for i := 0; i < idleWorkers; i++ {
		go idleLoop(quitIdle)
	}

	<-lg.quit
	close(quitBusy)
	close(quitIdle)
}

func busyLoop(quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		default:
			_ = math.Pow(2.0, 2.0)
		}
	}
}

func idleLoop(quit chan struct{}) {
	for {
		select {
		case <-quit:
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}
