package factory

import (
	"log"

	"github.com/komari-monitor/komari/pkg/item"
)

var (
	senders                = make(map[string]IMessageSender)
	senderConstructor      = make(map[string]MessageSenderConstructor)
	sendersAdditionalItems = make(map[string][]item.Item)
)

func RegisterMessageSender(constructor MessageSenderConstructor) {
	sender := constructor()
	senderConstructor[sender.GetName()] = constructor
	if sender == nil {
		panic("Message sender constructor returned nil")
	}
	if _, exists := senders[sender.GetName()]; exists {
		log.Println("Message sender already registered: " + sender.GetName())
	}
	senders[sender.GetName()] = sender

	// 使用反射来提取提供程序的配置字段
	config := sender.GetConfiguration()
	items := item.Parse(config)

	sendersAdditionalItems[sender.GetName()] = items
}

func GetSenderConfigs() map[string][]item.Item {
	return sendersAdditionalItems
}

func GetAllMessageSenders() map[string]IMessageSender {
	return senders
}

func GetConstructor(name string) (MessageSenderConstructor, bool) {
	constructor, exists := senderConstructor[name]
	return constructor, exists
}

func GetAllMessageSenderNames() []string {
	names := make([]string, 0, len(senders))
	for name := range senders {
		names = append(names, name)
	}
	return names
}

func Initialize() {
	for _, sender := range senders {
		if err := sender.Init(); err != nil {
			log.Printf("Failed to initialize message sender %s: %v", sender.GetName(), err)
		}
	}
}
