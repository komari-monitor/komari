package admin

import (
	"github.com/gin-gonic/gin"
	"github.com/komari-monitor/komari/api"
	"github.com/komari-monitor/komari/config"
	"github.com/komari-monitor/komari/database"
	"github.com/komari-monitor/komari/database/accounts"
	"github.com/komari-monitor/komari/database/models"
	"github.com/komari-monitor/komari/utils/oauth"
	"github.com/komari-monitor/komari/utils/oauth/factory"
)

func BindingExternalAccount(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	user, err := accounts.GetUserBySession(session)
	if err != nil {
		api.RespondError(c, 500, "No user found: "+err.Error())
		return
	}

	c.SetCookie("binding_external_account", user.UUID, 3600, "/", "", false, true)
	c.Redirect(302, "/api/oauth")
}
func UnbindExternalAccount(c *gin.Context) {
	session, _ := c.Cookie("session_token")
	user, err := accounts.GetUserBySession(session)
	if err != nil {
		api.RespondError(c, 500, "No user found: "+err.Error())
		return
	}

	err = accounts.UnbindExternalAccount(user.UUID)
	if err != nil {
		api.RespondError(c, 500, "Failed to unbind external account: "+err.Error())
		return
	}

	api.RespondSuccess(c, nil)
}

func GetOidcProvider(c *gin.Context) {
	provider := c.Query("provider")
	if provider != "" {
		// 如果指定了provider，返回单个提供者的配置
		config, err := database.GetOidcConfigByName(provider)
		if err != nil {
			api.RespondError(c, 404, "Provider not found: "+err.Error())
			return
		}
		api.RespondSuccess(c, config)
		return
	}
	// 否则返回所有提供者的配置
	providers := factory.GetProviderConfigs()
	if len(providers) == 0 {
		api.RespondError(c, 404, "No OIDC providers found")
		return
	}
	api.RespondSuccess(c, providers)
}

func SetOidcProvider(c *gin.Context) {
	var oidcConfig models.OidcProvider
	if err := c.ShouldBindJSON(&oidcConfig); err != nil {
		api.RespondError(c, 400, "Invalid configuration: "+err.Error())
		return
	}
	if oidcConfig.Name == "" {
		api.RespondError(c, 400, "Provider name is required")
		return
	}
	_, exists := factory.GetConstructor(oidcConfig.Name)
	if !exists {
		api.RespondError(c, 404, "Provider not found: "+oidcConfig.Name)
		return
	}

	if err := database.SaveOidcConfig(&oidcConfig); err != nil {
		api.RespondError(c, 500, "Failed to save OIDC provider configuration: "+err.Error())
		return
	}
	OAuthProvider, _ := config.GetAs[string](config.OAuthProviderKey, "github")
	// 正在使用，重载
	if OAuthProvider == oidcConfig.Name {
		err := oauth.LoadProvider(oidcConfig.Name, oidcConfig.Addition)
		if err != nil {
			api.RespondError(c, 500, "Failed to load OIDC provider: "+err.Error())
			return
		}
	}
	api.RespondSuccess(c, gin.H{"message": "OIDC provider set successfully"})
}
