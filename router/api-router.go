package router

import (
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/middleware"

	// Import oauth package to register providers via init()
	_ "github.com/QuantumNous/new-api/oauth"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
)

func SetApiRouter(router *gin.Engine) {
	apiRouter := router.Group("/api")
	apiRouter.Use(middleware.RouteTag("api"))
	// Artifact downloads preserve upstream byte ranges and HEAD metadata.
	apiRouter.Use(gzip.Gzip(gzip.DefaultCompression, gzip.WithExcludedPathsRegexs([]string{`^/api/task/self/[^/]+/artifacts/[^/]+/content$`})))
	apiRouter.Use(middleware.BodyStorageCleanup()) // 清理请求体存储
	apiRouter.Use(middleware.GlobalAPIRateLimit())
	anonymousRequestBodyLimit := middleware.AnonymousRequestBodyLimit()
	{
		apiRouter.GET("/setup", controller.GetSetup)
		apiRouter.POST("/setup", anonymousRequestBodyLimit, controller.PostSetup)
		apiRouter.GET("/status", controller.GetStatus)
		apiRouter.GET("/uptime/status", controller.GetUptimeKumaStatus)
		apiRouter.GET("/models", middleware.UserAuth(), controller.DashboardListModels)
		apiRouter.GET("/status/test", middleware.AdminAuth(), controller.TestStatus)
		apiRouter.GET("/notice", controller.GetNotice)
		apiRouter.GET("/user-agreement", controller.GetUserAgreement)
		apiRouter.GET("/privacy-policy", controller.GetPrivacyPolicy)
		apiRouter.GET("/about", controller.GetAbout)
		//apiRouter.GET("/midjourney", controller.GetMidjourney)
		apiRouter.GET("/home_page_content", controller.GetHomePageContent)
		apiRouter.GET("/pricing", middleware.HeaderNavModuleAuth("pricing"), controller.GetPricing)
		apiRouter.GET("/model_monitor", middleware.UserAuth(), controller.GetModelMonitor)
		apiRouter.GET("/model_monitor/availability", middleware.UserAuth(), controller.GetModelMonitorAvailability)
		apiRouter.GET("/membership/self", middleware.UserAuth(), controller.GetMembershipSelf)
		apiRouter.GET("/empty-responses/settings", middleware.RootAuth(), controller.GetEmptyResponseSettings)
		apiRouter.PUT("/empty-responses/settings", middleware.RootAuth(), controller.UpdateEmptyResponseSettings)
		apiRouter.GET("/membership/admin/tiers", middleware.AdminAuth(), controller.AdminGetMembershipTiers)
		apiRouter.PUT("/tickets/settings", middleware.RootAuth(), controller.UpdateTicketSettings)
		ticketRoute := apiRouter.Group("/tickets")
		ticketRoute.Use(middleware.UserAuth(), middleware.DisableCache())
		{
			ticketRoute.GET("/settings", controller.GetTicketSettings)
			ticketRoute.GET("/self", controller.GetMyTickets)
			ticketRoute.POST("/", middleware.TicketWriteRateLimit(), controller.CreateMyTicket)
			ticketRoute.GET("/:id", controller.GetMyTicket)
			ticketRoute.POST("/:id/replies", middleware.TicketWriteRateLimit(), controller.ReplyMyTicket)
		}
		ticketAdminRoute := apiRouter.Group("/tickets/admin")
		ticketAdminRoute.Use(middleware.AdminAuth(), middleware.DisableCache())
		{
			ticketAdminRoute.GET("/", controller.GetAdminTickets)
			ticketAdminRoute.GET("/:id", controller.GetAdminTicket)
			ticketAdminRoute.POST("/:id/replies", middleware.TicketWriteRateLimit(), controller.ReplyAdminTicket)
			ticketAdminRoute.PATCH("/:id/status", controller.UpdateAdminTicketStatus)
		}
		perfMetricsRoute := apiRouter.Group("/perf-metrics")
		perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
		{
			perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
			perfMetricsRoute.GET("", controller.GetPerfMetrics)
		}
		apiRouter.GET("/empty-responses/self", middleware.UserAuth(), controller.GetEmptyResponseStatus)
		apiRouter.GET("/empty-responses", middleware.UserAuth(), controller.ListEmptyResponses)
		apiRouter.POST("/empty-responses/claim", middleware.UserAuth(), middleware.CriticalRateLimit(), controller.ClaimEmptyResponses)

		apiRouter.GET("/rankings", middleware.HeaderNavModuleAuth("rankings"), controller.GetRankings)
		apiRouter.GET("/verification", middleware.EmailVerificationRateLimit(), middleware.TurnstileCheck(), controller.SendEmailVerification)
		apiRouter.GET("/reset_password", middleware.CriticalRateLimit(), middleware.TurnstileCheck(), controller.SendPasswordResetEmail)
		apiRouter.POST("/user/reset", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.ResetPassword)
		// OAuth routes - specific routes must come before :provider wildcard
		apiRouter.GET("/oauth/state", middleware.CriticalRateLimit(), controller.GenerateOAuthCode)
		apiRouter.POST("/oauth/email/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("email_bind"), anonymousRequestBodyLimit, controller.EmailBind)
		// Non-standard OAuth (WeChat, Telegram) - keep original routes
		apiRouter.GET("/oauth/wechat", middleware.CriticalRateLimit(), controller.WeChatAuth)
		apiRouter.POST("/oauth/wechat/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("wechat_bind"), anonymousRequestBodyLimit, controller.WeChatBind)
		apiRouter.GET("/oauth/telegram/login", middleware.CriticalRateLimit(), controller.TelegramLogin)
		apiRouter.GET("/oauth/telegram/bind", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("telegram_bind"), controller.TelegramBind)
		// Standard OAuth providers (GitHub, Discord, OIDC, LinuxDO) - unified route
		apiRouter.GET("/oauth/:provider", middleware.CriticalRateLimit(), controller.HandleOAuth)
		apiRouter.GET("/ratio_config", middleware.CriticalRateLimit(), controller.GetRatioConfig)

		apiRouter.POST("/stripe/webhook", anonymousRequestBodyLimit, controller.StripeWebhook)
		apiRouter.POST("/creem/webhook", anonymousRequestBodyLimit, controller.CreemWebhook)
		apiRouter.POST("/waffo/webhook", anonymousRequestBodyLimit, controller.WaffoWebhook)
		// :env separates test vs prod URLs so the operator can register each
		// in Pancake's matching webhook slot; handler enforces env match.
		apiRouter.POST("/waffo-pancake/webhook/:env", anonymousRequestBodyLimit, controller.WaffoPancakeWebhook)

		// Universal secure verification routes
		apiRouter.POST("/verify", middleware.UserAuth(), middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("secure_verify"), controller.UniversalVerify)

		userRoute := apiRouter.Group("/user")
		{
			userRoute.POST("/register", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Register)
			userRoute.GET("/login/encryption-key", middleware.DisableCache(), controller.GetPasswordEncryptionKey)
			userRoute.POST("/login", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, middleware.TurnstileCheck(), controller.Login)
			userRoute.POST("/login/2fa", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.Verify2FALogin)
			userRoute.POST("/passkey/login/begin", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.PasskeyLoginBegin)
			userRoute.POST("/passkey/login/finish", middleware.CriticalRateLimit(), anonymousRequestBodyLimit, controller.PasskeyLoginFinish)
			//userRoute.POST("/tokenlog", middleware.CriticalRateLimit(), controller.TokenLog)
			userRoute.GET("/logout", controller.Logout)
			userRoute.POST("/epay/notify", anonymousRequestBodyLimit, controller.EpayNotify)
			userRoute.GET("/epay/notify", controller.EpayNotify)
			userRoute.GET("/groups", controller.GetUserGroups)

			selfRoute := userRoute.Group("/")
			selfRoute.Use(middleware.UserAuth())
			{
				selfRoute.GET("/web-risk/status", controller.GetWebRiskStatus)
				selfRoute.POST("/web-risk/verify", middleware.CriticalRateLimit(), controller.VerifyWebRisk)
				selfRoute.GET("/self/groups", controller.GetUserGroups)
				selfRoute.GET("/self", controller.GetSelf)
				selfRoute.GET("/models", controller.GetUserModels)
				selfRoute.PUT("/self", middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("account_update"), middleware.DisableCache(), controller.UpdateSelf)
				selfRoute.DELETE("/self", middleware.AccountSecurityRateLimit("account_delete"), controller.DeleteSelf)
				selfRoute.GET("/token", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.GenerateManagedAccessToken)
				selfRoute.GET("/sessions", middleware.DisableCache(), controller.GetUserSessionsWithLimits)
				selfRoute.DELETE("/sessions/:id", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.DeleteUserSession)
				selfRoute.POST("/sessions/revoke-others", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RevokeOtherUserSessions)
				selfRoute.GET("/access-tokens", middleware.DisableCache(), controller.GetUserAccessTokensPage)
				selfRoute.POST("/access-tokens", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.CreateManagedUserAccessToken)
				selfRoute.PUT("/access-tokens/:id", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.UpdateUserAccessToken)
				selfRoute.DELETE("/access-tokens/:id", middleware.CriticalRateLimit(), middleware.DisableCache(), controller.RevokeManagedUserAccessToken)
				selfRoute.GET("/passkey", controller.PasskeyStatus)
				selfRoute.POST("/passkey/register/begin", middleware.AccountSecurityRateLimit("passkey_register"), controller.PasskeyRegisterBegin)
				selfRoute.POST("/passkey/register/finish", middleware.AccountSecurityRateLimit("passkey_register"), controller.PasskeyRegisterFinish)
				selfRoute.POST("/passkey/verify/begin", middleware.AccountSecurityRateLimit("passkey_verify"), controller.PasskeyVerifyBegin)
				selfRoute.POST("/passkey/verify/finish", middleware.AccountSecurityRateLimit("passkey_verify"), controller.PasskeyVerifyFinish)
				selfRoute.DELETE("/passkey", middleware.AccountSecurityRateLimit("passkey_delete"), controller.PasskeyDelete)
				selfRoute.GET("/aff", controller.GetAffCode)
				selfRoute.GET("/rebate", middleware.RebateReadRateLimit(), controller.GetUserRebateSummary)
				selfRoute.GET("/rebate/records", middleware.RebateReadRateLimit(), controller.GetUserRebateRecords)
				selfRoute.GET("/topup/info", controller.GetTopUpInfo)
				selfRoute.GET("/topup/self", controller.GetUserTopUps)
				selfRoute.POST("/topup", middleware.CriticalRateLimit(), controller.TopUp)
				selfRoute.POST("/pay", middleware.CriticalRateLimit(), controller.RequestEpay)
				selfRoute.POST("/amount", controller.RequestAmount)
				selfRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.RequestStripePay)
				selfRoute.POST("/stripe/amount", controller.RequestStripeAmount)
				selfRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.RequestCreemPay)
				selfRoute.POST("/waffo/amount", controller.RequestWaffoAmount)
				selfRoute.POST("/waffo/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPay)
				selfRoute.POST("/waffo-pancake/amount", controller.RequestWaffoPancakeAmount)
				selfRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.RequestWaffoPancakePay)
				selfRoute.POST("/aff_transfer", controller.TransferAffQuota)
				selfRoute.PUT("/setting", middleware.CriticalRateLimit(), controller.UpdateUserSetting)

				// 2FA routes
				selfRoute.GET("/2fa/status", controller.Get2FAStatus)
				selfRoute.POST("/2fa/setup", middleware.AccountSecurityRateLimit("twofa_setup"), controller.Setup2FA)
				selfRoute.POST("/2fa/enable", middleware.AccountSecurityRateLimit("twofa_enable"), controller.Enable2FA)
				selfRoute.POST("/2fa/disable", middleware.AccountSecurityRateLimit("twofa_disable"), controller.Disable2FA)
				selfRoute.POST("/2fa/backup_codes", middleware.AccountSecurityRateLimit("twofa_backup_codes"), controller.RegenerateBackupCodes)

				// Check-in routes
				selfRoute.GET("/checkin", controller.GetCheckinStatus)
				selfRoute.POST("/checkin", middleware.TurnstileCheck(), controller.DoCheckin)

				// Custom OAuth bindings
				selfRoute.GET("/oauth/bindings", controller.GetUserOAuthBindings)
				selfRoute.DELETE("/oauth/bindings/:provider_id", middleware.AccountSecurityRateLimit("oauth_bind"), controller.UnbindCustomOAuth)
			}

			adminRoute := userRoute.Group("/")
			adminRoute.Use(middleware.AdminAuth())
			{
				adminRoute.GET("/", controller.GetAllUsers)
				adminRoute.GET("/topup", controller.GetAllTopUps)
				adminRoute.POST("/topup/complete", controller.AdminCompleteTopUp)
				adminRoute.GET("/search", controller.SearchUsers)
				adminRoute.GET("/:id/oauth/bindings", controller.GetUserOAuthBindingsByAdmin)
				adminRoute.DELETE("/:id/oauth/bindings/:provider_id", middleware.AccountSecurityRateLimit("oauth_bind"), controller.UnbindCustomOAuthByAdmin)
				adminRoute.DELETE("/:id/bindings/:binding_type", middleware.AccountSecurityRateLimit("account_binding"), controller.AdminClearUserBinding)
				adminRoute.GET("/:id/group-ratios", controller.GetUserGroupRatios)
				adminRoute.PUT("/:id/group-ratios", controller.UpdateUserGroupRatio)
				adminRoute.DELETE("/:id/group-ratios", controller.DeleteUserGroupRatio)
				adminRoute.GET("/:id", controller.GetUser)
				adminRoute.POST("/", middleware.AccountSecurityRateLimit("account_create"), controller.CreateUser)
				adminRoute.POST("/manage", middleware.AccountSecurityRateLimit("account_manage"), controller.ManageUser)
				adminRoute.PUT("/", middleware.AccountSecurityRateLimit("account_update"), controller.UpdateUser)
				adminRoute.DELETE("/:id", middleware.AccountSecurityRateLimit("account_delete"), controller.DeleteUser)
				adminRoute.DELETE("/:id/reset_passkey", middleware.AccountSecurityRateLimit("passkey_delete"), controller.AdminResetPasskey)

				// Admin 2FA routes
				adminRoute.GET("/2fa/stats", controller.Admin2FAStats)
				adminRoute.DELETE("/:id/2fa", middleware.AccountSecurityRateLimit("twofa_disable"), controller.AdminDisable2FA)
			}
		}

		// Subscription billing (plans, purchase, admin management)
		subscriptionRoute := apiRouter.Group("/subscription")
		subscriptionRoute.Use(middleware.UserAuth())
		{
			subscriptionRoute.GET("/plans", controller.GetSubscriptionPlans)
			subscriptionRoute.GET("/self", controller.GetSubscriptionSelf)
			subscriptionRoute.PUT("/self/preference", controller.UpdateSubscriptionPreference)
			subscriptionRoute.POST("/balance/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestBalancePay)
			subscriptionRoute.POST("/epay/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestEpay)
			subscriptionRoute.POST("/stripe/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestStripePay)
			subscriptionRoute.POST("/creem/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestCreemPay)
			subscriptionRoute.POST("/waffo-pancake/pay", middleware.CriticalRateLimit(), controller.SubscriptionRequestWaffoPancakePay)
		}
		subscriptionAdminRoute := apiRouter.Group("/subscription/admin")
		subscriptionAdminRoute.Use(middleware.AdminAuth())
		{
			subscriptionAdminRoute.GET("/plans", controller.AdminListSubscriptionPlans)
			subscriptionAdminRoute.POST("/plans", controller.AdminCreateSubscriptionPlan)
			subscriptionAdminRoute.PUT("/plans/:id", controller.AdminUpdateSubscriptionPlan)
			subscriptionAdminRoute.PATCH("/plans/:id", controller.AdminUpdateSubscriptionPlanStatus)
			subscriptionAdminRoute.POST("/bind", controller.AdminBindSubscription)
			subscriptionAdminRoute.POST("/plans/:id/subscriptions/reset", controller.AdminResetPlanSubscriptions)

			// User subscription management (admin)
			subscriptionAdminRoute.GET("/users/:id/subscriptions", controller.AdminListUserSubscriptions)
			subscriptionAdminRoute.POST("/users/:id/subscriptions", controller.AdminCreateUserSubscription)
			subscriptionAdminRoute.POST("/users/:id/subscriptions/reset", controller.AdminResetUserSubscriptionsByPlan)
			subscriptionAdminRoute.POST("/user_subscriptions/:id/invalidate", controller.AdminInvalidateUserSubscription)
			subscriptionAdminRoute.DELETE("/user_subscriptions/:id", controller.AdminDeleteUserSubscription)
		}

		// Subscription payment callbacks (no auth)
		apiRouter.POST("/subscription/epay/notify", anonymousRequestBodyLimit, controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/notify", controller.SubscriptionEpayNotify)
		apiRouter.GET("/subscription/epay/return", controller.SubscriptionEpayReturn)
		apiRouter.POST("/subscription/epay/return", anonymousRequestBodyLimit, controller.SubscriptionEpayReturn)
		// Administrative rebate fraud response. Root-only: voiding cancels unpaid
		// obligations and clawback debits an inviter wallet, so both are
		// restricted to the installation owner and strictly rate limited.
		rebateAdminRoute := apiRouter.Group("/rebate/admin")
		rebateAdminRoute.Use(middleware.RootAuth(), middleware.CriticalRateLimit())
		{
			rebateAdminRoute.GET("/records", controller.AdminListUserRebates)
			rebateAdminRoute.POST("/void", controller.VoidUserRebates)
			rebateAdminRoute.POST("/clawback", controller.ClawbackUserRebates)
		}

		optionRoute := apiRouter.Group("/option")
		optionRoute.Use(middleware.RootAuth())
		{
			optionRoute.GET("/", controller.GetOptions)
			optionRoute.PUT("/", controller.UpdateOption)
			optionRoute.GET("/output-sensitive", middleware.DisableCache(), controller.GetOutputSensitiveConfig)
			optionRoute.PUT("/output-sensitive", controller.UpdateOutputSensitiveConfig)
			optionRoute.GET("/rebate", middleware.DisableCache(), controller.GetRebateConfig)
			optionRoute.PUT("/rebate", controller.UpdateRebateConfig)
			optionRoute.POST("/payment_compliance", controller.ConfirmPaymentCompliance)
			optionRoute.GET("/channel_affinity_cache", controller.GetChannelAffinityCacheStats)
			optionRoute.DELETE("/channel_affinity_cache", controller.ClearChannelAffinityCache)
			optionRoute.POST("/rest_model_ratio", controller.ResetModelRatio)
			optionRoute.POST("/migrate_console_setting", controller.MigrateConsoleSetting) // 用于迁移检测的旧键，下个版本会删除
			optionRoute.GET("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
			optionRoute.POST("/waffo-pancake/catalog", controller.ListWaffoPancakeCatalog)
			optionRoute.POST("/waffo-pancake/pair", controller.CreateWaffoPancakePair)
			optionRoute.POST("/waffo-pancake/save", controller.SaveWaffoPancake)
			optionRoute.POST("/waffo-pancake/subscription-product", controller.CreateWaffoPancakeSubscriptionProduct)
			optionRoute.GET("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
			optionRoute.POST("/waffo-pancake/subscription-product-options", controller.ListWaffoPancakeSubscriptionProductOptions)
			optionRoute.GET("/db_integrity", middleware.DisableCache(), controller.CheckDatabaseIntegrity)
			optionRoute.POST("/wallet_ledger/init", middleware.DisableCache(), controller.InitializeWalletLedger)
		}
		adminPermissionRoute := apiRouter.Group("/admin_permissions")
		adminPermissionRoute.Use(middleware.RootAuth())
		{
			adminPermissionRoute.GET("/", controller.ListAdminPermissions)
			adminPermissionRoute.PUT("/:id", controller.UpdateAdminPermissions)
		}

		// Custom OAuth provider management (root only)
		customOAuthRoute := apiRouter.Group("/custom-oauth-provider")
		customOAuthRoute.Use(middleware.RootAuth())
		{
			customOAuthRoute.POST("/discovery", controller.FetchCustomOAuthDiscovery)
			customOAuthRoute.GET("/", controller.GetCustomOAuthProviders)
			customOAuthRoute.GET("/:id", controller.GetCustomOAuthProvider)
			customOAuthRoute.POST("/", controller.CreateCustomOAuthProvider)
			customOAuthRoute.PUT("/:id", controller.UpdateCustomOAuthProvider)
			customOAuthRoute.DELETE("/:id", controller.DeleteCustomOAuthProvider)
		}
		performanceRoute := apiRouter.Group("/performance")
		performanceRoute.Use(middleware.RootAuth())
		{
			performanceRoute.GET("/stats", controller.GetPerformanceStats)
			performanceRoute.DELETE("/disk_cache", controller.ClearDiskCache)
			performanceRoute.POST("/reset_stats", controller.ResetPerformanceStats)
			performanceRoute.POST("/gc", controller.ForceGC)
			performanceRoute.GET("/logs", controller.GetLogFiles)
			performanceRoute.DELETE("/logs", controller.CleanupLogFiles)
		}
		ratioSyncRoute := apiRouter.Group("/ratio_sync")
		ratioSyncRoute.Use(middleware.RootAuth())
		{
			ratioSyncRoute.GET("/channels", controller.GetSyncableChannels)
			ratioSyncRoute.POST("/fetch", controller.FetchUpstreamRatios)
		}
		channelRoute := apiRouter.Group("/channel")
		channelRoute.Use(middleware.AdminAuth())
		{
			channelRoute.GET("/", controller.GetAllChannels)
			channelRoute.GET("/search", controller.SearchChannels)
			channelRoute.GET("/models", controller.ChannelListModels)
			channelRoute.GET("/models_enabled", controller.EnabledListModels)
			channelRoute.GET("/ops", controller.GetChannelOps)
			channelRoute.GET("/default_base_urls", controller.GetChannelDefaultBaseURLs)
			channelRoute.GET("/:id", middleware.DisableCache(), controller.GetChannel)
			channelRoute.POST("/:id/key", middleware.RootAuth(), middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("channel_key"), middleware.DisableCache(), middleware.SecureVerificationRequired(), controller.GetChannelKey)
			channelRoute.GET("/test", controller.TestAllChannels)
			channelRoute.GET("/test/:id", controller.TestChannel)
			channelRoute.GET("/update_balance", controller.UpdateAllChannelsBalance)
			channelRoute.GET("/update_balance/:id", controller.UpdateChannelBalance)
			channelRoute.POST("/", controller.AddChannel)
			channelRoute.PUT("/", controller.UpdateChannel)
			channelRoute.DELETE("/disabled", controller.DeleteDisabledChannel)
			channelRoute.POST("/tag/disabled", controller.DisableTagChannels)
			channelRoute.POST("/tag/enabled", controller.EnableTagChannels)
			channelRoute.PUT("/tag", controller.EditTagChannels)
			channelRoute.DELETE("/:id", controller.DeleteChannel)
			channelRoute.POST("/batch", controller.DeleteChannelBatch)
			channelRoute.POST("/fix", controller.FixChannelsAbilities)
			channelRoute.GET("/fetch_models/:id", controller.FetchUpstreamModels)
			channelRoute.POST("/fetch_models", middleware.RootAuth(), controller.FetchModels)
			channelRoute.POST("/codex/oauth/start", controller.StartCodexOAuth)
			channelRoute.POST("/codex/oauth/complete", controller.CompleteCodexOAuth)
			channelRoute.POST("/:id/codex/oauth/start", controller.StartCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/oauth/complete", controller.CompleteCodexOAuthForChannel)
			channelRoute.POST("/:id/codex/refresh", controller.RefreshCodexChannelCredential)
			channelRoute.GET("/:id/codex/usage", controller.GetCodexChannelUsage)
			channelRoute.POST("/ollama/pull", controller.OllamaPullModel)
			channelRoute.POST("/ollama/pull/stream", controller.OllamaPullModelStream)
			channelRoute.DELETE("/ollama/delete", controller.OllamaDeleteModel)
			channelRoute.GET("/ollama/version/:id", controller.OllamaVersion)
			channelRoute.POST("/batch/tag", controller.BatchSetChannelTag)
			channelRoute.GET("/tag/models", controller.GetTagModels)
			channelRoute.POST("/copy/:id", controller.CopyChannel)
			channelRoute.POST("/multi_key/manage", controller.ManageMultiKeys)
			channelRoute.POST("/upstream_updates/apply", controller.ApplyChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/apply_all", controller.ApplyAllChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect", controller.DetectChannelUpstreamModelUpdates)
			channelRoute.POST("/upstream_updates/detect_all", controller.DetectAllChannelUpstreamModelUpdates)
		}
		tokenRoute := apiRouter.Group("/token")
		tokenRoute.Use(middleware.UserAuth())
		{
			tokenRoute.GET("/", controller.GetAllTokens)
			tokenRoute.GET("/search", middleware.SearchRateLimit(), controller.SearchTokens)
			tokenRoute.GET("/:id", controller.GetToken)
			tokenRoute.POST("/:id/key", middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("token_key"), middleware.DisableCache(), controller.GetTokenKey)
			tokenRoute.POST("/", controller.AddToken)
			tokenRoute.PUT("/", controller.UpdateToken)
			tokenRoute.DELETE("/:id", controller.DeleteToken)
			tokenRoute.POST("/batch", controller.DeleteTokenBatch)
			tokenRoute.POST("/batch/keys", middleware.CriticalRateLimit(), middleware.AccountSecurityRateLimit("token_key"), middleware.DisableCache(), controller.GetTokenKeysBatch)
		}

		usageRoute := apiRouter.Group("/usage")
		usageRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			tokenUsageRoute := usageRoute.Group("/token")
			tokenUsageRoute.Use(middleware.TokenAuthReadOnly())
			{
				tokenUsageRoute.GET("/", controller.GetTokenUsage)
			}
		}

		redemptionRoute := apiRouter.Group("/redemption")
		redemptionRoute.Use(middleware.AdminAuth())
		{
			redemptionRoute.GET("/", controller.GetAllRedemptions)
			redemptionRoute.GET("/search", controller.SearchRedemptions)
			redemptionRoute.GET("/:id", controller.GetRedemption)
			redemptionRoute.POST("/", controller.AddRedemption)
			redemptionRoute.POST("/batch", controller.DeleteRedemptionBatch)
			redemptionRoute.PUT("/", controller.UpdateRedemption)
			redemptionRoute.DELETE("/invalid", controller.DeleteInvalidRedemption)
			redemptionRoute.DELETE("/:id", controller.DeleteRedemption)
		}
		logRoute := apiRouter.Group("/log")
		logRoute.GET("/settings", middleware.UserAuth(), middleware.DisableCache(), controller.GetLogSettings)
		logRoute.GET("/", middleware.AdminAuth(), controller.GetAllLogs)
		logRoute.DELETE("/", middleware.AdminAuth(), controller.DeleteHistoryLogs)
		logRoute.GET("/export", middleware.AdminAuth(), middleware.SearchRateLimit(), controller.ExportAllLogs)
		logRoute.GET("/self/export", middleware.UserAuth(), middleware.SearchRateLimit(), controller.ExportUserLogs)
		logRoute.POST("/cleanup", middleware.AdminAuth(), middleware.CriticalRateLimit(), controller.CreateLogCleanupTask)
		logRoute.GET("/cleanup/:id", middleware.AdminAuth(), controller.GetLogCleanupTask)
		logRoute.GET("/stat", middleware.AdminAuth(), controller.GetLogsStat)
		logRoute.GET("/self/stat", middleware.UserAuth(), controller.GetLogsSelfStat)
		logRoute.GET("/channel_affinity_usage_cache", middleware.AdminAuth(), controller.GetChannelAffinityUsageCacheStats)
		logRoute.GET("/search", middleware.AdminAuth(), controller.SearchAllLogs)
		logRoute.GET("/self", middleware.UserAuth(), controller.GetUserLogs)
		logRoute.GET("/self/search", middleware.UserAuth(), middleware.SearchRateLimit(), controller.SearchUserLogs)

		dataRoute := apiRouter.Group("/data")
		dataRoute.GET("/", middleware.AdminAuth(), controller.GetAllQuotaDates)
		dataRoute.GET("/users", middleware.AdminAuth(), controller.GetQuotaDatesByUser)
		dataRoute.GET("/self", middleware.UserAuth(), controller.GetUserQuotaDates)

		logRoute.Use(middleware.CORS(), middleware.CriticalRateLimit())
		{
			logRoute.GET("/token", middleware.TokenAuthReadOnly(), controller.GetLogByKey)
		}
		groupRoute := apiRouter.Group("/group")
		groupRoute.Use(middleware.AdminAuth())
		{
			groupRoute.GET("/", controller.GetGroups)
		}

		prefillGroupRoute := apiRouter.Group("/prefill_group")
		prefillGroupRoute.Use(middleware.AdminAuth())
		{
			prefillGroupRoute.GET("/", controller.GetPrefillGroups)
			prefillGroupRoute.POST("/", controller.CreatePrefillGroup)
			prefillGroupRoute.PUT("/", controller.UpdatePrefillGroup)
			prefillGroupRoute.DELETE("/:id", controller.DeletePrefillGroup)
		}

		mjRoute := apiRouter.Group("/mj")
		mjRoute.GET("/self", middleware.UserAuth(), controller.GetUserMidjourney)
		mjRoute.GET("/", middleware.AdminAuth(), controller.GetAllMidjourney)

		taskRoute := apiRouter.Group("/task")
		{
			taskRoute.GET("/self", middleware.UserAuth(), controller.GetUserTask)
			taskRoute.GET("/self/:task_id/artifacts", middleware.UserAuth(), controller.GetTaskArtifacts)
			taskRoute.GET("/self/:task_id/artifacts/:artifact_id/content", middleware.TokenOrUserAuth(), controller.GetTaskArtifactContent)
			taskRoute.HEAD("/self/:task_id/artifacts/:artifact_id/content", middleware.TokenOrUserAuth(), controller.GetTaskArtifactContent)
			pluginRoute := taskRoute.Group("/plugin")
			pluginRoute.Use(middleware.RootAuth())
			{
				pluginRoute.GET("", controller.ListTaskPlugins)
				pluginRoute.POST("", controller.CreateTaskPlugin)
				pluginRoute.GET("/schema", controller.GetTaskPluginSchema)
				pluginRoute.POST("/preview", controller.PreviewTaskPlugin)
				pluginRoute.GET("/bindings", controller.GetTaskPluginBindings)
				pluginRoute.PUT("/bindings", controller.UpdateTaskPluginBindings)
			}
			scriptPluginRoute := taskRoute.Group("/script_plugin")
			scriptPluginRoute.Use(middleware.RootAuth(), middleware.DisableCache())
			{
				scriptPluginRoute.GET("", controller.ListScriptPlugins)
				scriptPluginRoute.POST("", controller.CreateScriptPlugin)
				scriptPluginRoute.GET("/bindings", controller.ListScriptPluginBindings)
				scriptPluginRoute.PUT("/bindings", controller.PutScriptPluginBinding)
				scriptPluginRoute.GET("/marketplace/sources", controller.ListScriptPluginMarketSources)
				scriptPluginRoute.PUT("/marketplace/sources", middleware.CriticalRateLimit(), controller.PutScriptPluginMarketSources)
				scriptPluginRoute.GET("/marketplace/catalog", controller.GetScriptPluginMarketCatalog)
				scriptPluginRoute.POST("/marketplace/install", middleware.CriticalRateLimit(), controller.InstallScriptPluginMarketRevision)
				scriptPluginRoute.GET("/activations", controller.ListScriptPluginActivations)
				scriptPluginRoute.POST("/dryrun", middleware.CriticalRateLimit(), controller.DryRunScriptPlugin)
				scriptPluginRoute.POST("/:id/activate", middleware.CriticalRateLimit(), controller.ActivateScriptPlugin)
				scriptPluginRoute.DELETE("/:id", middleware.CriticalRateLimit(), controller.DeleteScriptPluginRevision)
				scriptPluginRoute.GET("/:id", controller.GetScriptPlugin)
				scriptPluginRoute.PUT("/:id/enabled", controller.SetScriptPluginEnabled)
			}
			taskRoute.GET("/", middleware.AdminAuth(), controller.GetAllTask)
		}

		vendorRoute := apiRouter.Group("/vendors")
		vendorRoute.Use(middleware.AdminAuth())
		{
			vendorRoute.GET("/", controller.GetAllVendors)
			vendorRoute.GET("/search", controller.SearchVendors)
			vendorRoute.GET("/:id", controller.GetVendorMeta)
			vendorRoute.POST("/", controller.CreateVendorMeta)
			vendorRoute.PUT("/", controller.UpdateVendorMeta)
			vendorRoute.DELETE("/:id", controller.DeleteVendorMeta)
		}

		modelsRoute := apiRouter.Group("/models")
		modelsRoute.Use(middleware.AdminAuth())
		{
			modelsRoute.GET("/sync_upstream/preview", controller.SyncUpstreamPreview)
			modelsRoute.POST("/sync_upstream", controller.SyncUpstreamModels)
			modelsRoute.GET("/missing", controller.GetMissingModels)
			modelsRoute.GET("/", controller.GetAllModelsMeta)
			modelsRoute.GET("/search", controller.SearchModelsMeta)
			modelsRoute.GET("/tags", controller.GetModelTags)
			modelsRoute.GET("/:id", controller.GetModelMeta)
			modelsRoute.POST("/", controller.CreateModelMeta)
			modelsRoute.PUT("/", controller.UpdateModelMeta)
			modelsRoute.POST("/batch/delete", controller.BatchDeleteModelMeta)
			modelsRoute.DELETE("/:id", controller.DeleteModelMeta)
		}

		// Deployments (model deployment management)
		deploymentsRoute := apiRouter.Group("/deployments")
		deploymentsRoute.Use(middleware.AdminAuth())
		{
			deploymentsRoute.GET("/settings", controller.GetModelDeploymentSettings)
			deploymentsRoute.POST("/settings/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/", controller.GetAllDeployments)
			deploymentsRoute.GET("/search", controller.SearchDeployments)
			deploymentsRoute.POST("/test-connection", controller.TestIoNetConnection)
			deploymentsRoute.GET("/hardware-types", controller.GetHardwareTypes)
			deploymentsRoute.GET("/locations", controller.GetLocations)
			deploymentsRoute.GET("/available-replicas", controller.GetAvailableReplicas)
			deploymentsRoute.POST("/price-estimation", controller.GetPriceEstimation)
			deploymentsRoute.GET("/check-name", controller.CheckClusterNameAvailability)
			deploymentsRoute.POST("/", controller.CreateDeployment)

			deploymentsRoute.GET("/:id", controller.GetDeployment)
			deploymentsRoute.GET("/:id/logs", controller.GetDeploymentLogs)
			deploymentsRoute.GET("/:id/containers", controller.ListDeploymentContainers)
			deploymentsRoute.GET("/:id/containers/:container_id", controller.GetContainerDetails)
			deploymentsRoute.PUT("/:id", controller.UpdateDeployment)
			deploymentsRoute.PUT("/:id/name", controller.UpdateDeploymentName)
			deploymentsRoute.POST("/:id/extend", controller.ExtendDeployment)
			deploymentsRoute.DELETE("/:id", controller.DeleteDeployment)
		}
	}
}
