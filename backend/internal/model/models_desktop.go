package model

import "time"

// Singleton is a database constraint, so concurrent first starts cannot create
// different local owners. User and marker are committed together.
type LocalProfile struct {
	Singleton int       `gorm:"primaryKey;autoIncrement:false" json:"-"`
	ID        string    `gorm:"uniqueIndex;size:36;not null" json:"id"`
	UserID    string    `gorm:"uniqueIndex;size:36;not null" json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
}

func (LocalProfile) TableName() string { return "local_profiles" }
