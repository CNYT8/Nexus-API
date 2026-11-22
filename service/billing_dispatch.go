package service

import relaycommon "github.com/QuantumNous/new-api/relay/common"

// MarkBillingDispatched 供各发送入口在上游请求之前保存预扣屏障。
func MarkBillingDispatched(info *relaycommon.RelayInfo) error {
	if info == nil || info.Billing == nil {
		return nil
	}
	if marker, ok := info.Billing.(interface{ MarkDispatched() error }); ok {
		return marker.MarkDispatched()
	}
	return nil
}
