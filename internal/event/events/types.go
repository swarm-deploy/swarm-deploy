package events

type Type string

const (
	TypeDeploySuccess     = "deploySuccess"
	TypeDeployFailed      = "deployFailed"
	TypeSyncManualStarted = "syncManualStarted"
)
