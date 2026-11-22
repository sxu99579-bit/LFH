package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"sort"
	"strings"
	"time"
)

// ScrubResult summarizes one background or on-demand bit-rot integrity scrub run.
type ScrubResult struct {
	Checked      int       `json:"checked"`
	OK           int       `json:"ok"`
	Corrupted    int       `json:"corrupted"`
	Missing      int       `json:"missing"`
	Backfilled   int       `json:"backfilled"`
	DurationMs   int64     `json:"duration_ms"`
	FinishedAt   time.Time `json:"finished_at"`
	CorruptedIDs []string  `json:"corrupted_ids,omitempty"`
}

// LastScrubResult returns the most recent integrity scrub summary, if any.
func (s *Store) LastScrubResult() *ScrubResult {
	s.scrubMu.RLock()
	defer s.scrubMu.RUnlock()
	if s.lastScrub == nil {
		return nil
	}
	cp := *s.lastScrub
	if len(s.lastScrub.CorruptedIDs) > 0 {
		cp.CorruptedIDs = append([]string(nil), s.lastScrub.CorruptedIDs...)
	}
	return &cp
}

// ScrubIntegrity verifies local file SHA-256 digests against stored metadata and backfills missing digests.
// Passing maxFiles <= 0 checks all records in the store.
func (s *Store) ScrubIntegrity(maxFiles int) ScrubResult {
	started := time.Now()
	type scrubCandidate struct {
		id       string
		diskPath string
		sha256   string
		external bool
		verified time.Time
	}

	s.mu.RLock()
	candidates := make([]scrubCandidate, 0, len(s.records))
	for id, rec := range s.records {
		var vt time.Time
		if rec.LastVerifiedAt != nil {
			vt = *rec.LastVerifiedAt
		}
		candidates = append(candidates, scrubCandidate{
			id:       id,
			diskPath: s.recordDiskPathLocked(rec),
			sha256:   rec.SHA256,
			external: rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount,
			verified: vt,
		})
	}
	s.mu.RUnlock()

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].verified.Equal(candidates[j].verified) {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].verified.Before(candidates[j].verified)
	})
	if maxFiles > 0 && len(candidates) > maxFiles {
		candidates = candidates[:maxFiles]
	}

	type scrubOutcome struct {
		id         string
		status     string
		shaHex     string
		missing    bool
		verifiedAt time.Time
	}

	res := ScrubResult{}
	outcomes := make([]scrubOutcome, 0, len(candidates))
	buf := make([]byte, 64*1024)

	for _, cand := range candidates {
		res.Checked++
		now := time.Now()
		f, err := os.Open(cand.diskPath)
		if err != nil {
			res.Missing++
			res.CorruptedIDs = append(res.CorruptedIDs, cand.id)
			s.RecordReadError()
			outcomes = append(outcomes, scrubOutcome{
				id:         cand.id,
				status:     "missing",
				missing:    cand.external,
				verifiedAt: now,
			})
			continue
		}
		h := sha256.New()
		_, copyErr := io.CopyBuffer(h, f, buf)
		_ = f.Close()
		if copyErr != nil {
			res.Corrupted++
			res.CorruptedIDs = append(res.CorruptedIDs, cand.id)
			s.RecordReadError()
			outcomes = append(outcomes, scrubOutcome{
				id:         cand.id,
				status:     "corrupted",
				verifiedAt: now,
			})
			continue
		}
		actualHex := hex.EncodeToString(h.Sum(nil))
		if strings.TrimSpace(cand.sha256) == "" {
			res.Backfilled++
			res.OK++
			outcomes = append(outcomes, scrubOutcome{
				id:         cand.id,
				status:     "ok",
				shaHex:     actualHex,
				verifiedAt: now,
			})
			continue
		}
		if strings.EqualFold(actualHex, cand.sha256) {
			res.OK++
			outcomes = append(outcomes, scrubOutcome{
				id:         cand.id,
				status:     "ok",
				verifiedAt: now,
			})
			continue
		}
		res.Corrupted++
		res.CorruptedIDs = append(res.CorruptedIDs, cand.id)
		s.RecordReadError()
		outcomes = append(outcomes, scrubOutcome{
			id:         cand.id,
			status:     "corrupted",
			verifiedAt: now,
		})
	}

	if len(outcomes) > 0 {
		s.mu.Lock()
		changed := false
		for _, out := range outcomes {
			rec, ok := s.records[out.id]
			if !ok {
				continue
			}
			vt := out.verifiedAt
			if out.shaHex != "" && rec.SHA256 == "" {
				rec.SHA256 = out.shaHex
			}
			rec.IntegrityStatus = out.status
			if rec.StorageMode == StorageModeRef || rec.StorageMode == StorageModeMount {
				rec.SourceMissing = out.missing
			}
			rec.LastVerifiedAt = &vt
			s.records[out.id] = rec
			changed = true
		}
		if changed {
			_ = s.saveLocked()
		}
		s.mu.Unlock()
	}

	res.FinishedAt = time.Now()
	res.DurationMs = res.FinishedAt.Sub(started).Milliseconds()
	s.scrubMu.Lock()
	cp := res
	if len(res.CorruptedIDs) > 0 {
		cp.CorruptedIDs = append([]string(nil), res.CorruptedIDs...)
	}
	s.lastScrub = &cp
	s.scrubMu.Unlock()
	return res
}
