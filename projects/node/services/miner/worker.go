package miner

const (
	GBTWorkerType  = "GBT_Worker"
	PoolWorkerType = "Pool_Worker"
)

type IWorker interface {
	GetType() string
	Start() error
	Stop()
	IsRunning() bool
	Update()
}
