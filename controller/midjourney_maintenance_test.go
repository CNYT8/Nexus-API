package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// Exercises the real initialization endpoint. A timed-out drain must never
// create the baseline marker, and deferred cleanup must reopen both gates.
func TestInitializeWalletLedgerAbortsOnDrainTimeout(t *testing.T) {
	for _, source := range []string{"worker", "http"} {
		t.Run(source, func(t *testing.T) {
			db := setupTopUpQuotaTestEnv(t).db
			middleware.EndWalletMaintenanceGate()
			service.SetRebateMaintenancePaused(false)
			t.Cleanup(func() {
				middleware.EndWalletMaintenanceGate()
				service.SetRebateMaintenancePaused(false)
			})
			router := gin.New()
			router.Use(middleware.WalletMaintenanceGate())
			router.POST("/api/option/wallet_ledger", InitializeWalletLedger)
			if source == "worker" {
				require.True(t, service.BeginTaskBillingWork())
				t.Cleanup(service.EndTaskBillingWork)
			} else {
				entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
				router.GET("/business", func(c *gin.Context) {
					close(entered)
					<-release
					c.Status(http.StatusOK)
				})
				go func() {
					defer close(done)
					router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/business", nil))
				}()
				<-entered
				t.Cleanup(func() {
					close(release)
					<-done
				})
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/option/wallet_ledger", nil))
			require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
			var markers, lots int64
			require.NoError(t, db.Model(&model.RebateSystemState{}).Count(&markers).Error)
			require.NoError(t, db.Model(&model.WalletCreditLot{}).Count(&lots).Error)
			require.Zero(t, markers)
			require.Zero(t, lots)
			require.False(t, middleware.IsWalletMaintenanceActive())
			require.True(t, service.BeginTaskBillingWork())
			service.EndTaskBillingWork()
		})
	}
}
