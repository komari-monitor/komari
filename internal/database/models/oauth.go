package models

type OidcProvider struct {
	Name     string `json:"name" gorm:"primaryKey;type:varchar(100);not null"`
	Addition string `json:"addition" gorm:"type:longtext" default:"{}"`
}
