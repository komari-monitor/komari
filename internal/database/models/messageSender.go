package models

import "time"

type MessageSenderProvider struct {
	Name     string `json:"name" gorm:"primaryKey;unique;not null"`
	Addition string `json:"addition" gorm:"type:longtext" default:"{}"`
}

type EventMessage struct {
	Event   string    `json:"event"`
	Clients []Client  `json:"clients"`
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
	Emoji   string    `json:"emoji"`
}
