// SPDX-License-Identifier: MIT
// Copyright (c) 2026 WoozyMasta
// Source: github.com/woozymasta/steamserverlist

package serverlist

import (
	"context"
	"fmt"
	"net/netip"
	"time"
)

// Discover queries a filter and builds a reusable bounded partition plan.
func (c *Client) Discover(
	ctx context.Context,
	root QueryFilter,
	options DiscoveryOptions,
) (DiscoveryResult[Server], error) {
	return discover(ctx, c, root, options, decodeDiscoveryServers)
}

// DiscoverLite queries a filter and returns compact records and a reusable plan.
func (c *Client) DiscoverLite(
	ctx context.Context,
	root QueryFilter,
	options DiscoveryOptions,
) (DiscoveryResult[ServerLite], error) {
	return discover(ctx, c, root, options, decodeDiscoveryLite)
}

// DiscoverAddresses queries a filter and returns unique endpoints and a reusable plan.
func (c *Client) DiscoverAddresses(
	ctx context.Context,
	root QueryFilter,
	options DiscoveryOptions,
) (DiscoveryResult[netip.AddrPort], error) {
	return discover(ctx, c, root, options, decodeDiscoveryAddresses)
}

// Collect executes the active leaves of a saved plan and returns rich records.
func (c *Client) Collect(
	ctx context.Context,
	plan DiscoveryPlan,
	options CollectOptions,
) (Result[Server], error) {
	return collect(ctx, c, plan, options, decodeDiscoveryServers)
}

// CollectLite executes a saved plan and returns compact records.
func (c *Client) CollectLite(
	ctx context.Context,
	plan DiscoveryPlan,
	options CollectOptions,
) (Result[ServerLite], error) {
	return collect(ctx, c, plan, options, decodeDiscoveryLite)
}

// CollectAddresses executes a saved plan and returns unique endpoints.
func (c *Client) CollectAddresses(
	ctx context.Context,
	plan DiscoveryPlan,
	options CollectOptions,
) (Result[netip.AddrPort], error) {
	return collect(ctx, c, plan, options, decodeDiscoveryAddresses)
}

// discoveryRun holds mutable state for one [discover] or [collect] operation.
type discoveryRun[T any] struct {
	// ctx cancels all requests in this operation.
	ctx context.Context
	// err stops further requests after the first failure.
	err error
	// client executes each planned query.
	client *Client
	// decode selects the retained output projection.
	decode func([]byte) ([]discoveryRecord[T], error)
	// diagnostics receives synchronous progress events.
	diagnostics func(DiscoveryEvent)
	// indices maps endpoints to deduplicated output slots.
	indices map[netip.AddrPort]int
	// plan is expanded by discovery and read by collection.
	plan DiscoveryPlan
	// items retains one result per unique endpoint.
	items []T
	// options bounds this discovery operation.
	options DiscoveryOptions
	// requests counts attempted API calls.
	requests int
	// buffered tracks records and witnesses held during traversal.
	buffered int
}

// A coverage path tracks only missing endpoints and their original split side.
// Evidence belongs to a branch, never to the global output deduplication map.
type discoveryCoverage struct {
	// missing maps sampled endpoints to their expected match side.
	missing map[netip.AddrPort]bool
	// predicate identifies the split being checked.
	predicate Predicate
}

// discoveryCoveragePath associates an ancestor split with the branch being traversed.
type discoveryCoveragePath struct {
	// coverage is the ancestor's outstanding sample evidence.
	coverage *discoveryCoverage
	// matched selects the positive or complement child.
	matched bool
}

// discover runs the initial query, partitions capped pages, and returns a reusable plan.
func discover[T any](
	ctx context.Context,
	client *Client,
	root QueryFilter,
	options DiscoveryOptions,
	decode func([]byte) ([]discoveryRecord[T], error),
) (DiscoveryResult[T], error) {
	if ctx == nil {
		return DiscoveryResult[T]{}, ErrNilContext
	}
	if err := validateDiscoveryRoot(root); err != nil {
		return DiscoveryResult[T]{}, err
	}
	if err := normalizeDiscoveryOptions(&options); err != nil {
		return DiscoveryResult[T]{}, err
	}

	run := discoveryRun[T]{
		ctx:         ctx,
		client:      client,
		decode:      decode,
		indices:     make(map[netip.AddrPort]int),
		options:     options,
		plan:        newDiscoveryPlan(root, options.Limit),
		diagnostics: options.Diagnostics,
	}
	records := run.request(root.String(), options.Limit, nil)
	run.walk(run.plan.tree, root, records, 0, nil)
	_, unresolved, reasons := planStatus(run.plan.tree)

	return DiscoveryResult[T]{
		Plan: run.plan,
		Result: Result[T]{
			Items: run.items,
			Status: DiscoveryStatus{
				Requests:   run.requests,
				Unresolved: unresolved,
				Reasons:    reasons,
			}},
	}, run.err
}

// collect executes saved plan leaves within the optional request budget.
func collect[T any](
	ctx context.Context,
	client *Client,
	plan DiscoveryPlan,
	options CollectOptions,
	decode func([]byte) ([]discoveryRecord[T], error),
) (Result[T], error) {
	if ctx == nil {
		return Result[T]{}, ErrNilContext
	}
	if options.MaxRequests < 0 {
		return Result[T]{}, fmt.Errorf("%w: negative request budget", ErrInvalidDiscoveryOptions)
	}
	queries, err := plan.executableQueries()
	if err != nil {
		return Result[T]{}, err
	}

	budget := len(queries)
	if options.MaxRequests > 0 {
		budget = min(budget, options.MaxRequests)
	}

	run := discoveryRun[T]{
		ctx:         ctx,
		client:      client,
		decode:      decode,
		plan:        plan,
		indices:     make(map[netip.AddrPort]int),
		diagnostics: options.Diagnostics,
	}

	// Collection cannot revalidate the semantic assumptions established during planning.
	_, _, historical := planStatus(plan.tree)
	status := DiscoveryStatus{
		Reasons: historical & (DiscoveryReasonInconsistent | DiscoveryReasonUnverifiedSemantics),
	}

	for index, query := range queries {
		if index == budget {
			status.Reasons |= DiscoveryReasonRequestBudget
			status.Unresolved += len(queries) - index
			break
		}

		// Each successful page is committed immediately, so a later request failure preserves partial results.
		records := run.request(query.Filter, query.Limit, nil)
		if run.err != nil {
			status.Reasons |= DiscoveryReasonRequestFailure
			status.Unresolved += len(queries) - index
			break
		}

		run.buffered -= len(records)
		reason := rootAppIDIssue(plan.root, records)
		if discoveryNearLimit(len(records), int(query.Limit)) {
			reason |= DiscoveryReasonSaturated
		}

		if reason != 0 {
			status.Unresolved++
		}

		status.Reasons |= reason
	}

	status.Requests = run.requests

	return Result[T]{Items: run.items, Status: status}, run.err
}

// walk refines one already-fetched page. Recursion is bounded to 64 levels.
// A parent is replaced only after both children succeed;
// coverage is checked after descendants drain
// because capped child pages need not include its sample.
func (run *discoveryRun[T]) walk(
	node *discoveryPlanNode,
	filter QueryFilter,
	records []discoveryRecord[T],
	depth int,
	paths []discoveryCoveragePath,
) bool {
	pageSize := len(records)
	defer func() { run.buffered -= pageSize }()
	if run.err != nil {
		node.Reasons |= DiscoveryReasonRequestFailure
		return false
	}
	if issue := rootAppIDIssue(run.plan.root, records); issue != 0 {
		node.Reasons |= issue
		return false
	}

	if !discoveryNearLimit(len(records), int(run.options.Limit)) {
		return true
	}

	node.Reasons = DiscoveryReasonSaturated
	leaves, _, _ := planStatus(run.plan.tree)

	// Resource checks run before the next split so a stopped leaf remains executable later.
	switch {
	case depth >= run.options.MaxDepth:
		node.Reasons |= DiscoveryReasonDepthLimit

	case leaves >= run.options.MaxPlanQueries:
		node.Reasons |= DiscoveryReasonPlanQueryBudget

	case run.requests+2 > run.options.MaxRequests:
		node.Reasons |= DiscoveryReasonRequestBudget

	case run.buffered+2*int(run.options.Limit) > run.options.MaxBufferedRecords:
		node.Reasons |= DiscoveryReasonBufferBudget
	}

	if node.Reasons != DiscoveryReasonSaturated {
		return false
	}

	candidate, ok := chooseDiscoverySplit(records, filter, run.options.Features, run.options.MinValueCount)
	if !ok {
		node.Reasons |= DiscoveryReasonNoUsefulSplit
		if hasUnknownFeatureEvidence(records, filter, run.options.Features) {
			node.Reasons |= DiscoveryReasonUnverifiedSemantics
		}
		return false
	}

	yesFilter, yesErr := filter.withPredicate(candidate)
	noFilter, noErr := filter.withNORPredicate(candidate)
	if yesErr != nil || noErr != nil {
		node.Reasons |= DiscoveryReasonNoUsefulSplit
		return false
	}

	coverage := &discoveryCoverage{
		predicate: candidate,
		missing:   make(map[netip.AddrPort]bool, len(records)),
	}
	for _, record := range records {
		// Remember each endpoint's expected side before child responses can replace its metadata.
		coverage.missing[record.witness.addr] = witnessMatches(record.witness, candidate)
	}

	// Transfer the page's accounting to the deduplicated coverage map.
	// Replace the page count with unique coverage keys;
	// duplicate endpoints need only one witness.
	run.buffered += len(coverage.missing) - pageSize
	pageSize = 0
	defer func() { run.buffered -= len(coverage.missing) }()

	// Copy the ancestry once per side: siblings must never share a mutable path.
	yesPath := append(
		append([]discoveryCoveragePath(nil), paths...),
		discoveryCoveragePath{coverage: coverage, matched: true},
	)
	noPath := append(
		append([]discoveryCoveragePath(nil), paths...),
		discoveryCoveragePath{coverage: coverage, matched: false},
	)
	yes := run.request(yesFilter.String(), run.options.Limit, yesPath)

	var no []discoveryRecord[T]
	if run.err == nil {
		no = run.request(noFilter.String(), run.options.Limit, noPath)
	}
	if run.err != nil {
		run.buffered -= len(yes) + len(no)
		node.Reasons |= DiscoveryReasonRequestFailure
		return false
	}

	issue := rootAppIDIssue(run.plan.root, yes, no)
	if !splitMembershipKnown(yes, no, candidate) {
		issue |= DiscoveryReasonUnverifiedSemantics
	} else if !validDiscoveryChildren(yes, no, candidate) {
		issue |= DiscoveryReasonInconsistent
	}
	if issue != 0 {
		run.buffered -= len(yes) + len(no)
		node.Reasons |= issue
		return false
	}

	*node = discoveryPlanNode{
		Split:    &candidate,
		Match:    &discoveryPlanNode{Active: true},
		NonMatch: &discoveryPlanNode{Active: true},
	}
	yesDrained := run.walk(node.Match, yesFilter, yes, depth+1, yesPath)
	noDrained := run.walk(node.NonMatch, noFilter, no, depth+1, noPath)

	// Missing endpoints in still-capped branches are unresolved, not contradictory.
	mismatches := 0
	for _, matched := range coverage.missing {
		if matched && yesDrained || !matched && noDrained {
			mismatches++
		}
	}
	if mismatches > 0 {
		run.reportCoverage(filter, coverage, yesDrained, noDrained, mismatches)
		_, _, reasons := planStatus(node)
		*node = discoveryPlanNode{
			Active:  true,
			Reasons: reasons | DiscoveryReasonSaturated | DiscoveryReasonInconsistent,
		}
		return false
	}

	return yesDrained && noDrained
}

// request commits each successful page once, before any branch validation.
// Last observation replaces metadata; first observation determines output order.
func (run *discoveryRun[T]) request(filter string, limit uint32, paths []discoveryCoveragePath) []discoveryRecord[T] {
	var started time.Time
	if run.diagnostics != nil {
		started = time.Now()
	}

	records, err := listDecoded(
		run.ctx,
		run.client,
		ListQuery{Filter: filter, Limit: limit},
		run.decode,
	)
	run.requests++

	if err == nil && len(records) > int(limit) {
		err = fmt.Errorf("%w: requested %d, received %d", ErrDiscoveryResponseOverLimit, limit, len(records))
	}
	if err != nil {
		run.err = err
		records = nil
	}

	before := len(run.items)
	var empty T
	for index := range records {
		record := &records[index]
		address := record.witness.addr

		if slot, exists := run.indices[address]; exists {
			run.items[slot] = record.item
		} else {
			run.indices[address] = len(run.items)
			run.items = append(run.items, record.item)
		}

		// The output owns this item now; release the page's duplicate reference before recursion.
		record.item = empty
		for _, path := range paths {
			expected, exists := path.coverage.missing[address]
			if exists &&
				expected == path.matched &&
				witnessMatches(record.witness, path.coverage.predicate) == path.matched {
				delete(path.coverage.missing, address)
				run.buffered--
			}
		}
	}

	run.buffered += len(records)
	if run.diagnostics != nil {
		leaves, _, _ := planStatus(run.plan.tree)
		event := DiscoveryEvent{
			Filter:          filter,
			Request:         run.requests,
			DurationMS:      time.Since(started).Milliseconds(),
			ReturnedRows:    len(records),
			NewEndpoints:    len(run.items) - before,
			ActiveLeaves:    leaves,
			BufferedRecords: run.buffered,
		}
		if err != nil {
			event.Error = err.Error()
			event.Reason = DiscoveryReasonRequestFailure
		} else if discoveryNearLimit(len(records), int(limit)) {
			event.Reason = DiscoveryReasonSaturated
		}

		run.diagnostics(event)
	}

	return records
}

// reportCoverage emits a bounded sample of endpoints missing from drained child branches.
func (run *discoveryRun[T]) reportCoverage(
	filter QueryFilter,
	coverage *discoveryCoverage,
	yesDrained, noDrained bool,
	count int,
) {
	if run.diagnostics == nil {
		return
	}

	event := DiscoveryEvent{
		Filter:          filter.String(),
		Request:         run.requests,
		ReturnedRows:    count,
		Reason:          DiscoveryReasonInconsistent,
		BufferedRecords: run.buffered,
	}

	for address, matched := range coverage.missing {
		if matched && yesDrained || !matched && noDrained {
			event.Mismatches = append(event.Mismatches, DiscoveryMismatch{
				Address: address,
				Key:     coverage.predicate.Key,
				Value:   coverage.predicate.Value,
				Matched: matched,
			})
			if len(event.Mismatches) == 32 {
				break
			}
		}
	}

	run.diagnostics(event)
}
