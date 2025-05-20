package config

import "github.com/spf13/viper"

func NewViper() *viper.Viper {
	v := viper.New()
	v.SetConfigFile(".env")
	_ = v.ReadInConfig()
	v.AutomaticEnv()
	return v
}
