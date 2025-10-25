package factory

import "github.com/komari-monitor/komari/database/models"

type IMessageSender interface {
	GetName() string
	// 请务必返回 &Configuration{} 的指针
	GetConfiguration() Configuration
	SendTextMessage(message, title string) error
	Init() error
	Destroy() error
}

// IEventMessageSender 是可选接口,如果实现则可以接收结构化的事件消息
type IEventMessageSender interface {
	SendEvent(event models.EventMessage) error
}

type Configuration interface{}

type MessageSenderConstructor func() IMessageSender
