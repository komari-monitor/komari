package models

type Clipboard struct {
	Id        int       `json:"id" gorm:"primaryKey;autoIncrement;unique"`
	Text      string    `json:"text" gorm:"type:longtext"`
	Name      string    `json:"name" gorm:"type:varchar(255)"`
	Weight    int       `json:"weight" gorm:"type:int"`
	Remark    string    `json:"remark" gorm:"type:text"`
	CreatedAt LocalTime `json:"created_at"`
	UpdatedAt LocalTime `json:"updated_at"`
}
