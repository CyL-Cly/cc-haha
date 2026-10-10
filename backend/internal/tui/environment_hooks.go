package tui

type EnvironmentHookSource interface {
	SubscribeEnvironmentHookNotices(func(Event)) func()
}
