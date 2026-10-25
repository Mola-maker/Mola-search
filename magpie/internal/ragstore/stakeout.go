package ragstore

import (
	"context"

	"github.com/yetone/magpie/internal/searchdet"
)

// Stakeout persistence. The saved jobs live in the primary SQLite store's
// stakeout_jobs table, beside the corpus they watch. Scheduling is not this
// package's job: searchdet owns the timer that re-runs a query and diffs the
// result; ragstore only makes sure a job survives a restart with its last
// snapshot intact, which is what the next diff needs.

// StakeoutList returns every saved job, ordered by id so the list is stable
// between calls.
func (m *Manager) StakeoutList(ctx context.Context) ([]searchdet.StakeoutJob, error) {
	return m.primary.stakeoutList(ctx)
}

// StakeoutSave writes one job, replacing any with the same id.
func (m *Manager) StakeoutSave(ctx context.Context, j searchdet.StakeoutJob) error {
	return m.primary.stakeoutSave(ctx, j)
}

// StakeoutDelete removes one job. Deleting a job that isn't there is not an
// error: the caller's intent (the job is gone) already holds.
func (m *Manager) StakeoutDelete(ctx context.Context, id string) error {
	return m.primary.stakeoutDelete(ctx, id)
}

// StakeoutGet returns one job and whether it exists.
func (m *Manager) StakeoutGet(ctx context.Context, id string) (searchdet.StakeoutJob, bool, error) {
	return m.primary.stakeoutGet(ctx, id)
}
