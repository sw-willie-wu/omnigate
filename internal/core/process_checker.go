package core

// ProcessChecker is an optional capability: providers that can detect whether
// their game is currently running implement this. Callers type-assert
// (mirrors core.ExeNamer / core.CheckForUpdateProgress / core.PathProvider).
type ProcessChecker interface {
	IsGameRunning(gid GameID) (bool, error)
}
