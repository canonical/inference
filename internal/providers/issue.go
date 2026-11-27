package providers

import "fmt"

type Issue struct {
	Details string
}

func newSharedProviderIssue(format string, args ...any) Issue {
	return Issue{Details: "Cannot use shared provider: " + fmt.Sprintf(format, args...)}
}
