package models

type Task struct {
	TaskId  string       `json:"task_id" gorm:"type:varchar(36);primaryKey;unique"`
	Clients StringArray  `json:"clients" gorm:"type:longtext"`
	Command string       `json:"command" gorm:"type:text"`
	Results []TaskResult `gorm:"foreignKey:TaskId;references:TaskId;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
}

type TaskResult struct {
	TaskId     string     `json:"task_id" gorm:"type:varchar(36);index"`
	Client     string     `json:"client" gorm:"type:varchar(36)"`
	ClientInfo Client     `json:"client_info" gorm:"foreignKey:Client;references:UUID;constraint:OnDelete:CASCADE,OnUpdate:CASCADE"`
	Result     string     `json:"result" gorm:"type:longtext"`
	ExitCode   *int       `json:"exit_code" gorm:"type:int"`
	FinishedAt *LocalTime `json:"finished_at" gorm:"type:timestamp"`
	CreatedAt  LocalTime  `json:"created_at" gorm:"type:timestamp"`
}
