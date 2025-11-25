package empty

import (
	"fmt"

	"github.com/komari-monitor/komari/internal/messageSender/factory"
)

type Addition struct {
}

type EmptyProvider struct {
	Addition
}

func (e *EmptyProvider) GetName() string {
	return "empty"
}

func (e *EmptyProvider) GetConfiguration() factory.Configuration {
	return &e.Addition
}

func (e *EmptyProvider) Init() error {
	return nil
}

func (e *EmptyProvider) Destroy() error {
	return nil
}

func (e *EmptyProvider) SendTextMessage(message, title string) error {
	return fmt.Errorf("you are using an empty message sender, please check your configuration")
}

func init() {
	factory.RegisterMessageSender(func() factory.IMessageSender {
		return &EmptyProvider{}
	})
}

// 确保实现了 IMessageSender 接口
var _ factory.IMessageSender = (*EmptyProvider)(nil)
