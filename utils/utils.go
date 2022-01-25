package utils

import (
	"fmt"
	"log"
	"os"
)

var (
	LogPath               = "./logs/"
	TpuLog                = "tpu_executor"
	BackendLog            = "backend"
	Executor              = "executor"
	CachePath             = "./cache/"
	ConfigPath            = "./config/"
	TokensFile            = ConfigPath + "tokens.json"
	UsersFile             = ConfigPath + "tokens_user.json"
	UsersSimulateFile     = ConfigPath + "tokens_user_simulate.json"
	UsersOwnerFile        = ConfigPath + "users_owner.json"
	UserOwnerSimulateFile = ConfigPath + "users_owner_simulate.json"
	MarketsFile           = ConfigPath + "markets.json"
	ConfigFile            = ConfigPath + "config.json"
	ValidatorFile         = ConfigPath + "validator.json"
)

func NewLog(dir, name string) *log.Logger {
	fileName := fmt.Sprintf("%s%s.log", dir, name)
	file, err := os.OpenFile(fileName, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}
	log := log.New(file, "", log.LstdFlags|log.Lmicroseconds)
	return log
}

func ReverseBytes(dst []byte, src []byte) {
	for i := 0; i < len(src); i++ {
		dst[len(src)-1-i] = src[i]
	}
}
