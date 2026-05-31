package factory

import "github.com/gin-gonic/gin"

type IOidcProvider interface {
	GetName() string
	// 请务必返回 &Configuration{} 的指针
	GetConfiguration() Configuration
	// 获取授权URL和状态
	GetAuthorizationURL(redirectURI string) (string, string)
	OnCallback(ctx *gin.Context, state string, query map[string]string, callbackURI string) (OidcCallback, error)
	Init() error
	Destroy() error
}

type OidcCallback struct {
	UserId string
}

type Configuration interface{}

type OidcConstructor func() IOidcProvider
