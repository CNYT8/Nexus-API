package main

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// runWalletMaintenance executes an explicit, operator-driven wallet credit
// maintenance action and returns so the caller can shut down. Accounting
// writers MUST be stopped and drained before "init"; otherwise a mixed-version
// fleet could create balances that are not backed by source lots.
//
//	NEXUS_WALLET_MAINTENANCE=init    snapshot legacy balances as non-eligible lots
//	NEXUS_WALLET_MAINTENANCE=verify  check quota == lots - open debt for every user
func runWalletMaintenance(action string) {
	switch action {
	case "init":
		active, err := model.WalletCreditLedgerActive(model.DB)
		if err != nil {
			common.FatalLog("wallet credit maintenance failed to read baseline state: " + err.Error())
		}
		if active {
			common.SysLog("wallet credit baseline already initialized; no changes made")
			return
		}
		if err := model.InitializeWalletCreditBaseline(model.DB); err != nil {
			common.FatalLog("wallet credit baseline initialization failed: " + err.Error())
		}
		common.SysLog("wallet credit baseline initialized: legacy positive balances were recorded as non-eligible lots")
	case "verify":
		afterUserID := 0
		const batchSize = 2000
		var mismatches int
		for {
			batch, next, done, err := model.ReconcileWalletCreditBatch(model.DB, afterUserID, batchSize)
			if err != nil {
				common.FatalLog("wallet credit reconciliation failed: " + err.Error())
			}
			for _, item := range batch {
				mismatches++
				common.SysLog(fmt.Sprintf(
					"wallet credit mismatch: user=%d quota=%d lots=%d held=%d open_debt=%d expected=%d",
					item.UserID, item.Quota, item.LotRemaining, item.HeldQuota, item.OpenDebt, item.ExpectedQuota,
				))
			}
			if done {
				break
			}
			afterUserID = next
		}
		if mismatches > 0 {
			common.FatalLog(fmt.Sprintf("wallet credit reconciliation found %d mismatched users; resolve them before enabling rebates", mismatches))
		}
		common.SysLog("wallet credit reconciliation passed: every wallet balance is backed by source lots minus open debt")
	default:
		common.FatalLog("unknown NEXUS_WALLET_MAINTENANCE action: " + action + " (expected init or verify)")
	}
}
