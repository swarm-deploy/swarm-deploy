package events

type AwareUser interface {
	WithUsername(username string) Event
}
