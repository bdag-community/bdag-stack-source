package bdagEci

import (
	"eci/bdagEci/aliyun"
	"eci/config"
	"eci/types"
	"log"
)

type EciProvider interface {
	Init()
	CreateContainer()
	DeleteContainer(containerId any)
	AllContainers(containerIds any, result any)
	RestartContainers(containerId any)
	SetConfig(conf *config.Config)
}

func NewEciInstance(conf *config.Config) EciProvider {
	var ep EciProvider
	switch conf.EciType {
	case types.ECI_TYPE_ALIYUN:
		ep = &aliyun.AliyunECI{}
	default:
		log.Fatalln("Not Support Eci Provider", conf.EciType)
	}
	ep.SetConfig(conf)
	ep.Init()
	return ep
}
