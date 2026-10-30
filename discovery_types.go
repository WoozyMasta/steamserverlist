// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import "net/netip"

const (
	// DiscoveryReasonRequestBudget marks work skipped after the request budget.
	DiscoveryReasonRequestBudget DiscoveryReasons = 1 << iota
	// DiscoveryReasonPlanQueryBudget marks work blocked by the terminal-leaf cap.
	DiscoveryReasonPlanQueryBudget
	// DiscoveryReasonBufferBudget marks work skipped by the witness-memory cap.
	DiscoveryReasonBufferBudget
	// DiscoveryReasonDepthLimit marks a branch stopped at the maximum split depth.
	DiscoveryReasonDepthLimit
	// DiscoveryReasonNoUsefulSplit marks a saturated branch without a safe split.
	DiscoveryReasonNoUsefulSplit
	// DiscoveryReasonSaturated marks an active leaf that may be truncated.
	DiscoveryReasonSaturated
	// DiscoveryReasonInconsistent marks contradictory split observations.
	DiscoveryReasonInconsistent
	// DiscoveryReasonUnverifiedSemantics marks a split lacking membership evidence.
	DiscoveryReasonUnverifiedSemantics
	// DiscoveryReasonRequestFailure marks an interrupted or failed request.
	DiscoveryReasonRequestFailure
)

// DiscoveryOptions controls bounded partition discovery.
type DiscoveryOptions struct {
	// Diagnostics receives synchronous request and decision events when set.
	Diagnostics func(DiscoveryEvent)
	// Features selects filter keys the planner may use; zero uses defaults.
	Features []SplitFeature
	// MaxRequests bounds all requests made by one discovery run.
	MaxRequests int
	// MaxPlanQueries bounds the terminal queries retained for future collection.
	MaxPlanQueries int
	// MaxBufferedRecords bounds compact witnesses retained for branch validation.
	MaxBufferedRecords int
	// MaxDepth bounds the number of nested binary partitions.
	MaxDepth int
	// MinValueCount excludes values with insufficient sample support.
	MinValueCount int
	// Limit is the per-request result cap; zero uses [DefaultLimit].
	Limit uint32
}

// CollectOptions controls execution of an existing discovery plan.
type CollectOptions struct {
	// Diagnostics receives synchronous request events when set.
	Diagnostics func(DiscoveryEvent)
	// MaxRequests bounds collection; zero allows all active plan queries.
	MaxRequests int
}

// DiscoveryEvent is an optional per-request diagnostic delivered synchronously.
type DiscoveryEvent struct {
	// Error contains a redacted request error, when one occurred.
	Error string `json:"error,omitempty" yaml:"error,omitempty"`
	// Filter is the expression used by the request or the checked parent branch.
	Filter string `json:"filter" yaml:"filter"`
	// Mismatches contains at most 32 sampled endpoints missing from a drained split side.
	Mismatches []DiscoveryMismatch `json:"mismatches,omitempty" yaml:"mismatches,omitempty"`
	// DurationMS is request execution and decoding time; coverage-only events use zero.
	DurationMS int64 `json:"duration_ms" yaml:"duration_ms"`
	// Request is the one-based number of attempted requests when the event is emitted.
	Request int `json:"request" yaml:"request"`
	// ReturnedRows is the response row count, or the missing-endpoint count for coverage events.
	ReturnedRows int `json:"returned_rows" yaml:"returned_rows"`
	// NewEndpoints is the number of previously unseen endpoints added by this response.
	NewEndpoints int `json:"new_endpoints" yaml:"new_endpoints"`
	// ActiveLeaves is the current executable plan-leaf count for request events.
	ActiveLeaves int `json:"active_leaves" yaml:"active_leaves"`
	// BufferedRecords is the current number of records retained for split validation.
	BufferedRecords int `json:"buffered_records" yaml:"buffered_records"`
	// Reason identifies a request or coverage condition associated with the event.
	Reason DiscoveryReasons `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// DiscoveryMismatch identifies a parent-sample endpoint not recovered from
// the drained child branch expected to contain it.
type DiscoveryMismatch struct {
	// Address is the endpoint observed in the parent sample.
	Address netip.AddrPort `json:"address" yaml:"address"`
	// Key is the filter field used to partition the parent sample.
	Key FilterKey `json:"key" yaml:"key"`
	// Value is the candidate operand used for the partition.
	Value string `json:"value" yaml:"value"`
	// Matched reports which child branch the parent sample assigned this endpoint to.
	Matched bool `json:"matched" yaml:"matched"`
}

// Result contains records from every successful query in one operation.
type Result[T any] struct {
	// Items contains the unique records retained from successful responses.
	Items []T `json:"items" yaml:"items"`
	// Status describes request and partition work that remains unresolved.
	Status DiscoveryStatus `json:"status" yaml:"status"`
}

// DiscoveryResult contains a first-run result and its reusable partition plan.
type DiscoveryResult[T any] struct {
	// Plan stores the filters for later collection, not the observed records.
	Plan DiscoveryPlan `json:"plan" yaml:"plan"`
	// Result contains records and status from the discovery requests.
	Result[T]
}

// DiscoveryStatus describes current operational coverage
// without claiming a coherent global Steam snapshot.
type DiscoveryStatus struct {
	// Requests is the number of attempted HTTP requests.
	Requests int `json:"requests" yaml:"requests"`
	// Unresolved counts plan branches or collection queries not drained successfully.
	Unresolved int `json:"unresolved" yaml:"unresolved"`
	// Reasons is the combined set of conditions that prevented a clean drain.
	Reasons DiscoveryReasons `json:"reasons" yaml:"reasons"`
}

// Complete reports whether all planned partitions were operationally drained.
func (status DiscoveryStatus) Complete() bool {
	return status.Requests > 0 && status.Unresolved == 0 && status.Reasons == 0
}

// SplitFeature enables a built-in filter key for candidate partitioning.
type SplitFeature struct {
	// Key selects one supported field for partition candidates.
	Key FilterKey `json:"key"    yaml:"key"`
	// Weight favors or penalizes a feature in the sampled balance score.
	Weight float64 `json:"weight" yaml:"weight"`
}

// DefaultSplitFeatures returns the package's default discovery properties.
func DefaultSplitFeatures() []SplitFeature {
	return []SplitFeature{
		{Key: KeyMap, Weight: 1},
		{Key: KeyVersion, Weight: 1},
		{Key: KeyGameType, Weight: 1.2},
		{Key: KeyLinux, Weight: 0.8},
		{Key: KeySecure, Weight: 0.5},
		{Key: KeyDedicated, Weight: 0.2},
	}
}

// DiscoveryReasons describes independent reasons discovery
// could not establish an operationally drained result.
type DiscoveryReasons uint16

// Has reports whether all bits in reason are present.
func (reasons DiscoveryReasons) Has(reason DiscoveryReasons) bool {
	return reasons&reason == reason
}
