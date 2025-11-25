package models

type OidcProvider struct {
	Name     string `json:"name" gorm:"primaryKey;unique;not null"`
	Addition string `json:"addition" gorm:"type:longtext" default:"{}"`
}
