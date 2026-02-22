package domain

// LegPolicy defines how multi-leg signal groups are executed.
type LegPolicy string

const (
	LegPolicyAllOrNone  LegPolicy = "all_or_none"  // cancel all if any leg fails
	LegPolicyBestEffort LegPolicy = "best_effort"  // place all, accept partials
	LegPolicySequential LegPolicy = "sequential"  // wait for each leg before next
)
