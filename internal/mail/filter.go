package mail

import "context"

type Filter struct {
	ID       string
	Criteria FilterCriteria
	Action   FilterAction
}

type FilterCriteria struct {
	From           string
	To             string
	Subject        string
	Query          string
	NegatedQuery   string
	HasAttachment  bool
	ExcludeChats   bool
	Size           int64
	SizeComparison string
}

type FilterAction struct {
	AddLabelIDs    []string
	RemoveLabelIDs []string
	Forward        string
}

type FilterManager interface {
	Filters(context.Context) ([]Filter, error)
	CreateFilter(context.Context, Filter) (Filter, error)
	DeleteFilter(context.Context, string) error
}
