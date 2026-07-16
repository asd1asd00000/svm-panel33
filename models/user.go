package models

import "time"

type User struct {
	ID         int
	Username   string
	Password   string
	ExpiryDate time.Time
	DataLimit  int64 // به بایت
	DataUsed   int64 // به بایت
}
