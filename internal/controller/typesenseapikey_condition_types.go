package controller

// Definitions to manage TypesenseApiKey status conditions.
const (
	ApiKeyConditionTypeReady = "Ready"

	ApiKeyConditionReasonReconciliationInProgress = "ReconciliationInProgress"
	ApiKeyConditionReasonClusterNotFound          = "ClusterNotFound"
	ApiKeyConditionReasonAdminKeySecretNotReady   = "AdminKeySecretNotReady"
	ApiKeyConditionReasonKeyCreateFailed          = "KeyCreateFailed"
	ApiKeyConditionReasonKeyRotateFailed          = "KeyRotateFailed"
	ApiKeyConditionReasonKeyDriftCheckFailed      = "KeyDriftCheckFailed"
	ApiKeyConditionReasonSecretNotReady           = "SecretNotReady"
	ApiKeyConditionReasonReady                    = "Ready"

	ApiKeyInitReconciliationMessage = "Starting reconciliation"
	ApiKeyUpdateStatusMessageFailed = "failed to update typesense api key status"
)
