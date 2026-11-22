package model

// A model is registered once with its migration phase. Both startup paths and
// the read-only integrity report consume this registry; special migrations must
// not disappear from the report merely because they cannot use AutoMigrate.
type modelSpec struct {
	Name  string
	Model interface{}
}

type migrationPhase uint8

const (
	migrationOrdinary migrationPhase = iota
	migrationCompatibility
	migrationAuthentication
	migrationSubscription
	migrationAccounting
)

type registeredModel struct {
	modelSpec
	Phase migrationPhase
}

func primaryDBRegistry() []registeredModel {
	return []registeredModel{
		{modelSpec{"Channel", &Channel{}}, migrationOrdinary},
		{modelSpec{"Token", &Token{}}, migrationOrdinary},
		{modelSpec{"User", &User{}}, migrationOrdinary},
		{modelSpec{"PasskeyCredential", &PasskeyCredential{}}, migrationOrdinary},
		{modelSpec{"LoginEncryptionKey", &LoginEncryptionKey{}}, migrationOrdinary},
		{modelSpec{"Option", &Option{}}, migrationOrdinary},
		{modelSpec{"Redemption", &Redemption{}}, migrationOrdinary},
		{modelSpec{"Ability", &Ability{}}, migrationOrdinary},
		{modelSpec{"Log", &Log{}}, migrationOrdinary},
		{modelSpec{"Midjourney", &Midjourney{}}, migrationOrdinary},
		{modelSpec{"TopUp", &TopUp{}}, migrationOrdinary},
		{modelSpec{"QuotaData", &QuotaData{}}, migrationOrdinary},
		{modelSpec{"Task", &Task{}}, migrationOrdinary},
		{modelSpec{"SystemTask", &SystemTask{}}, migrationCompatibility},
		{modelSpec{"LogCleanupLease", &LogCleanupLease{}}, migrationOrdinary},
		{modelSpec{"LogCleanupCheckpoint", &LogCleanupCheckpoint{}}, migrationOrdinary},
		{modelSpec{"TaskPlugin", &TaskPlugin{}}, migrationCompatibility},
		{modelSpec{"ScriptPlugin", &ScriptPlugin{}}, migrationOrdinary},
		{modelSpec{"ScriptPluginBinding", &ScriptPluginBinding{}}, migrationOrdinary},
		{modelSpec{"ScriptPluginActivation", &ScriptPluginActivation{}}, migrationOrdinary},
		{modelSpec{"ScriptPluginTombstone", &ScriptPluginTombstone{}}, migrationOrdinary},
		{modelSpec{"ScriptPluginMarketSource", &ScriptPluginMarketSource{}}, migrationOrdinary},
		{modelSpec{"Model", &Model{}}, migrationOrdinary},
		{modelSpec{"Vendor", &Vendor{}}, migrationOrdinary},
		{modelSpec{"PrefillGroup", &PrefillGroup{}}, migrationOrdinary},
		{modelSpec{"Setup", &Setup{}}, migrationOrdinary},
		{modelSpec{"TwoFA", &TwoFA{}}, migrationOrdinary},
		{modelSpec{"TwoFABackupCode", &TwoFABackupCode{}}, migrationOrdinary},
		{modelSpec{"Checkin", &Checkin{}}, migrationOrdinary},
		{modelSpec{"SubscriptionPlan", &SubscriptionPlan{}}, migrationSubscription},
		{modelSpec{"SubscriptionOrder", &SubscriptionOrder{}}, migrationOrdinary},
		{modelSpec{"UserSubscription", &UserSubscription{}}, migrationOrdinary},
		{modelSpec{"SubscriptionPreConsumeRecord", &SubscriptionPreConsumeRecord{}}, migrationOrdinary},
		{modelSpec{"CustomOAuthProvider", &CustomOAuthProvider{}}, migrationOrdinary},
		{modelSpec{"UserOAuthBinding", &UserOAuthBinding{}}, migrationOrdinary},
		{modelSpec{"PerfMetric", &PerfMetric{}}, migrationOrdinary},
		{modelSpec{"ModelMonitorSample", &ModelMonitorSample{}}, migrationOrdinary},
		{modelSpec{"UserMembership", &UserMembership{}}, migrationOrdinary},
		{modelSpec{"MembershipQuotaGrant", &MembershipQuotaGrant{}}, migrationOrdinary},
		{modelSpec{"UserGroupRatioOverride", &UserGroupRatioOverride{}}, migrationOrdinary},
		{modelSpec{"Ticket", &Ticket{}}, migrationOrdinary},
		{modelSpec{"TicketMessage", &TicketMessage{}}, migrationOrdinary},
		{modelSpec{"WebRiskState", &WebRiskState{}}, migrationOrdinary},
		{modelSpec{"EmptyResponseRecord", &EmptyResponseRecord{}}, migrationOrdinary},
		{modelSpec{"UserSession", &UserSession{}}, migrationCompatibility},
		{modelSpec{"AuthFlow", &AuthFlow{}}, migrationCompatibility},
		{modelSpec{"UserAccessToken", &UserAccessToken{}}, migrationCompatibility},
		{modelSpec{"SchemaCompatibilityMigration", &SchemaCompatibilityMigration{}}, migrationCompatibility},
		{modelSpec{"AccountSecurityLimit", &AccountSecurityLimit{}}, migrationAuthentication},
		{modelSpec{"WalletCreditLot", &WalletCreditLot{}}, migrationAccounting},
		{modelSpec{"WalletConsumptionSettlement", &WalletConsumptionSettlement{}}, migrationAccounting},
		{modelSpec{"WalletConsumptionAllocation", &WalletConsumptionAllocation{}}, migrationAccounting},
		{modelSpec{"WalletReservation", &WalletReservation{}}, migrationAccounting},
		{modelSpec{"WalletLotHold", &WalletLotHold{}}, migrationAccounting},
		{modelSpec{"WalletDebtRepayment", &WalletDebtRepayment{}}, migrationAccounting},
		{modelSpec{"RebateCapBucket", &RebateCapBucket{}}, migrationAccounting},
		{modelSpec{"RebateSystemState", &RebateSystemState{}}, migrationAccounting},
		{modelSpec{"RebateRecord", &RebateRecord{}}, migrationAccounting},
		{modelSpec{"WalletSourceReversal", &WalletSourceReversal{}}, migrationAccounting},
		{modelSpec{"RebateSourceReversal", &RebateSourceReversal{}}, migrationAccounting},
		{modelSpec{"TopUpPaymentBinding", &TopUpPaymentBinding{}}, migrationAccounting},
		{modelSpec{"TopUpReversalState", &TopUpReversalState{}}, migrationAccounting},
		{modelSpec{"TopUpReversalEvent", &TopUpReversalEvent{}}, migrationAccounting},
	}
}

func primaryDBModelSpecs() []modelSpec {
	var specs []modelSpec
	for _, entry := range primaryDBRegistry() {
		specs = append(specs, entry.modelSpec)
	}
	return specs
}

func modelsForMigration(phase migrationPhase) []interface{} {
	var models []interface{}
	for _, entry := range primaryDBRegistry() {
		if entry.Phase == phase {
			models = append(models, entry.Model)
		}
	}
	return models
}

// Relational log databases do not allocate ClickHouse IDs. Their metadata set
// deliberately excludes LogIDSequence, unlike the ClickHouse auxiliary DB.
func logMetadataModelSpecs(clickHouse bool) []modelSpec {
	specs := []modelSpec{
		{"ModelMonitorSample", &ModelMonitorSample{}},
		{"LogCleanupLease", &LogCleanupLease{}},
		{"LogCleanupCheckpoint", &LogCleanupCheckpoint{}},
	}
	if clickHouse {
		specs = append(specs, modelSpec{"LogIDSequence", &LogIDSequence{}})
	}
	return specs
}

func relationalLogDBModelSpecs() []modelSpec {
	return append([]modelSpec{{"Log", &Log{}}}, logMetadataModelSpecs(false)...)
}

func logDBModelSpecs() []modelSpec {
	if IsClickHouseLogDB() {
		return []modelSpec{{"Log", &Log{}}}
	}
	return relationalLogDBModelSpecs()
}
