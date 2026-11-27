// ABOUTME: Resolves Claude background-fork lineage against sibling transcripts.
// ABOUTME: Plans the replayed-prefix trim for background-forked session files (#1370).
package parser

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/tidwall/gjson"
)

// Claude Code's background handoff (left-arrow picker, Ctrl+B,
// /background) spawns `claude --resume <transcript> --fork-session` with
// CLAUDE_CODE_SESSION_KIND=bg. The forked process re-persists the entire
// prior message chain into a new transcript in the same project
// directory: replayed entries keep their original uuid, timestamp,
// message id, and usage, while sessionId is rewritten and
// sessionKind:"bg" is stamped on every chain entry. Older releases leave
// no pointer between the two transcripts, so lineage is established from
// content overlap.
//
// Claude Code 2.1.332 also writes pointers. A handoff appends a uuid-less
// continued-in record naming the new session to the transcript it leaves.
// /fork spawns the same kind of bg replay, which opens with a
// <fork-source> task notification, and records in the forking transcript
// a local_command whose stdout ends with the child's 8-character id
// prefix. The continued-in record and the child's own notification are
// never replayed, so they identify a parent and a fork. The /fork stdout
// is a chain entry that later replays copy, so it can only rule a named
// session out as an ancestor: a replayed /fork line names a sibling
// forked off an ancestor, never the transcript's own parent.
//
// A replay is written in one burst, so its replayed lines are contiguous
// uuid-bearing lines. The original writer interleaves its own uuid-less
// records (API request logs, last-prompt, titles). A candidate whose
// shared run with the fork extends past the fork's leading contiguous
// block is therefore not the source of that replay: it is the fork's
// descendant, and trimming against it would erase the fork's own turns.
//
// Direction is anchored on the asymmetric bg stamp: only a transcript
// whose root chain entry is bg-marked is ever considered a fork. The
// stamp is process-derived — the writer re-stamps sessionKind from the
// current process on every persisted line, so a non-bg fork of a bg
// transcript carries no marker and never trims. Both non-bg and bg
// siblings qualify as ancestors, so chained backgrounding trims each
// link against its nearest ancestor, but a bg candidate that strictly
// contains the fork never wins: it is the fork's descendant or an
// extension, and trimming against it could erase the ancestor. Equal
// uuid content (a fork that is a pure copy of a bg sibling) elects
// direction deterministically by stem. Anything else ambiguous
// (unmarked manual --fork-session copies, missing or divergent
// siblings) fails open: no trim and no relationship is emitted,
// leaving the status-quo duplicate rather than risking a
// wrongly-oriented trim.
//
// The replayed lines stay in the parse, marked replayed like a /branch
// head: they produce no messages, but the fork's own records hang off
// them. A fork can rewind into the copied history, which leaves several
// of its own branches hanging off the copy, and DAG branch selection
// decides which one the session keeps, as it does for any rewind.

const (
	// claudeLineageSniffMaxLines bounds how many leading lines are
	// scanned for a transcript's first chain entry. Real transcripts
	// carry at most a few non-chain records (summaries, ai-title,
	// mode, queue-operations) before the first uuid-bearing entry.
	claudeLineageSniffMaxLines = 256
	// claudeSniffCacheMaxEntries bounds the shared sniff memo. The
	// cache is rebuilt lazily after a reset, so overflow only costs
	// re-reads of transcript heads.
	claudeSniffCacheMaxEntries = 8192
	// claudeSniffDigestBytes bounds the transcript prefix hashed to
	// verify every metadata-matching sniff-cache entry. Size, mtime and
	// ctime can all match after a rapid same-length rewrite,
	// so those hits require an unchanged leading digest too.
	// Heads whose parse runs past the bound are not memoized.
	claudeSniffDigestBytes = 64 << 10
)

// claudeHeadSniff summarizes the first uuid-bearing chain entry of a
// transcript head.
type claudeHeadSniff struct {
	rootUUID string
	rootIsBG bool
	ok       bool
}

type claudeSniffCacheEntry struct {
	size    int64
	mtimeNS int64
	headSHA [sha256.Size]byte
	sniff   claudeHeadSniff
}

// The sniff memo is package-level because providers are re-instantiated
// for every classification and parse pass; per-instance state would
// never get cache hits.
var (
	claudeSniffMu    sync.Mutex
	claudeSniffCache = map[string]claudeSniffCacheEntry{}
)

// claudeParseOptions gates opt-in parse behaviors for providers that reuse the
// Claude transcript pipeline.
type claudeParseOptions struct {
	ctx                         context.Context
	persistedOutputPathResolver func(string) (string, bool)
	// siblingLineage enables background-fork lineage resolution
	// against sibling transcripts in the same directory.
	siblingLineage bool
	// uploadIdentity enables adoption of explicit rooted transcript IDs.
	uploadIdentity bool
	// compatibleTitleEvents enables the sessionName and ai-title fields
	// written by compatible transcript producers and ranks custom-title
	// ahead of them.
	compatibleTitleEvents bool
	// aiTitleFallback enables native Claude ai-title metadata. A user rename
	// (/rename command or custom-title) keeps priority; sessionName remains a
	// compatible-only field.
	aiTitleFallback bool
}

// claudeLineagePlan describes an established fork lineage: the leading
// replayCount uuid-bearing lines of the fork transcript are a replay of
// the parent transcript. The parse keeps them in the DAG, marked
// replayed, and stores none of them as the fork's messages.
type claudeLineagePlan struct {
	parentSessionID string
	// relationship is RelFork for a /fork child and RelContinuation for
	// a handoff or an unmarked replay.
	relationship RelationshipType
	replayCount  int
}

func claudeSniffHead(
	ctx context.Context, path string,
) (claudeHeadSniff, error) {
	if err := ctx.Err(); err != nil {
		return claudeHeadSniff{}, err
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return claudeHeadSniff{}, nil //nolint:nilerr // Unavailable optional lineage metadata yields no lineage hint.
	}
	claudeSniffMu.Lock()
	e, cached := claudeSniffCache[path]
	claudeSniffMu.Unlock()
	if cached && e.size == info.Size() && e.mtimeNS == info.ModTime().UnixNano() {
		// Matching metadata does not prove matching content: a
		// same-length rewrite can restore mtime while ctime also stays
		// unchanged. Verify the bounded leading-byte digest on every
		// metadata hit before reusing a root UUID or background stamp.
		headSHA, ok := claudeHeadDigest(path)
		if ok && headSHA == e.headSHA {
			return e.sniff, nil
		}
	}

	sniff, headSHA, verified, err := claudeSniffHeadUncached(ctx, path)
	if err != nil {
		return claudeHeadSniff{}, err
	}
	if err := ctx.Err(); err != nil {
		return claudeHeadSniff{}, err
	}
	if verified {
		claudeSniffMu.Lock()
		if len(claudeSniffCache) >= claudeSniffCacheMaxEntries {
			claudeSniffCache = map[string]claudeSniffCacheEntry{}
		}
		claudeSniffCache[path] = claudeSniffCacheEntry{
			size:    info.Size(),
			mtimeNS: info.ModTime().UnixNano(),
			headSHA: headSHA,
			sniff:   sniff,
		}
		claudeSniffMu.Unlock()
	}
	return sniff, nil
}

// claudeHeadDigest hashes the leading claudeSniffDigestBytes of
// path. ok is false when the file cannot be opened or read.
func claudeHeadDigest(path string) ([sha256.Size]byte, bool) {
	f, err := os.Open(path)
	if err != nil {
		return [sha256.Size]byte{}, false
	}
	defer f.Close()
	headSHA, _, ok := claudeHashLeading(f, claudeSniffDigestBytes)
	return headSHA, ok
}

// claudeHashLeading hashes the leading limit bytes of f without
// moving the file offset, returning the digest and the number of
// bytes hashed. ok is false when the read fails, including for
// files that do not support positioned reads.
func claudeHashLeading(
	f *os.File, limit int64,
) ([sha256.Size]byte, int64, bool) {
	h := sha256.New()
	n, err := io.Copy(h, io.NewSectionReader(f, 0, limit))
	if err != nil {
		return [sha256.Size]byte{}, 0, false
	}
	var headSHA [sha256.Size]byte
	copy(headSHA[:], h.Sum(nil))
	return headSHA, n, true
}

func claudeSniffHeadUncached(
	ctx context.Context, path string,
) (claudeHeadSniff, [sha256.Size]byte, bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return claudeHeadSniff{}, [sha256.Size]byte{}, false, nil
	}
	defer f.Close()
	// Digest the head before parsing it, from the same open file, so
	// the stored digest cannot describe content newer than the
	// parsed lines. A rewrite racing between the digest read and the
	// parse can still pair a digest with lines it did not cover; the
	// settled file then hashes differently and the next lookup
	// misses, so the mismatch cannot be served as a hit. The parse is
	// memoized only when it consumed bytes inside the digested
	// prefix: beyond it, a rewrite could hide from the digest.
	headSHA, hashed, digestOK := claudeHashLeading(f, claudeSniffDigestBytes)
	lr := newLineReaderContext(ctx, f, maxLineSize)
	defer releaseLineReader(lr)
	sniff, consumed, err := claudeSniffHeadLines(ctx, lr)
	verified := digestOK && consumed <= hashed
	return sniff, headSHA, verified, err
}

func claudeSniffHeadLines(
	ctx context.Context, lr *lineReader,
) (claudeHeadSniff, int64, error) {
	for range claudeLineageSniffMaxLines {
		if err := ctx.Err(); err != nil {
			return claudeHeadSniff{}, lr.bytesRead, err
		}
		lineBytes, ok := lr.nextBytes()
		if !ok {
			if lr.Err() != nil {
				if err := ctx.Err(); err != nil {
					return claudeHeadSniff{}, lr.bytesRead, err
				}
			}
			return claudeHeadSniff{}, lr.bytesRead, nil
		}
		if !gjson.ValidBytes(lineBytes) {
			continue
		}
		uuid := gjson.GetBytes(lineBytes, "uuid").Str
		if uuid == "" {
			continue
		}
		// The first chain entry of a well-formed transcript (or of a
		// replayed copy, which starts at the conversation root) has no
		// parentUuid. Any other head shape is unexpected: fail open.
		if gjson.GetBytes(lineBytes, "parentUuid").Str != "" {
			return claudeHeadSniff{}, lr.bytesRead, nil
		}
		return claudeHeadSniff{
			rootUUID: uuid,
			rootIsBG: gjson.GetBytes(lineBytes, "sessionKind").Str == SessionKindBackground,
			ok:       true,
		}, lr.bytesRead, nil
	}
	if err := ctx.Err(); err != nil {
		return claudeHeadSniff{}, lr.bytesRead, err
	}
	return claudeHeadSniff{}, lr.bytesRead, nil
}

// claudeLineageMarks summarizes the lineage evidence of one transcript
// beyond its uuid sequence.
type claudeLineageMarks struct {
	// contiguous counts the leading uuid-bearing lines that no uuid-less
	// record interrupts. Records before the first uuid-bearing line do not
	// count as interruptions.
	contiguous int
	// continuedIn lists the sessions that continued-in records name.
	continuedIn []string
	// forkChildPrefixes lists the session id prefixes that /fork stdout
	// lines name.
	forkChildPrefixes []string
	// forkSourceAt lists, for each <fork-source> notification, how many
	// uuid-bearing lines precede it.
	forkSourceAt []int
}

// namesChild reports whether the marks name stem as a session this
// transcript handed off to or forked.
func (m claudeLineageMarks) namesChild(stem string) bool {
	if m.continues(stem) {
		return true
	}
	for _, prefix := range m.forkChildPrefixes {
		if strings.HasPrefix(stem, prefix) {
			return true
		}
	}
	return false
}

// continues reports whether a continued-in record names stem.
func (m claudeLineageMarks) continues(stem string) bool {
	return slices.Contains(m.continuedIn, stem)
}

// forkSourceAfter reports whether a <fork-source> notification follows
// the first n uuid-bearing lines, outside a replay of that length.
func (m claudeLineageMarks) forkSourceAfter(n int) bool {
	for _, at := range m.forkSourceAt {
		if at >= n {
			return true
		}
	}
	return false
}

// claudeForkSourceMarker opens the task notification a /fork child
// receives first.
var claudeForkSourceMarker = []byte("<fork-source>")

// claudeIsForkSourceNotice reports whether a line is the <fork-source> task
// notification a /fork child opens with: the queue record that enqueues it,
// or the user record that delivers it. Other lines can quote the marker,
// such as a tool result showing a search for it, so the notification must
// be the record's own content rather than text anywhere on the line.
func claudeIsForkSourceNotice(lineBytes []byte, entryType string) bool {
	if !bytes.Contains(lineBytes, claudeForkSourceMarker) {
		return false
	}
	var body string
	switch entryType {
	case "queue-operation":
		body = gjson.GetBytes(lineBytes, "content").Str
	case "user":
		if gjson.GetBytes(lineBytes, "origin.kind").Str != "task-notification" {
			return false
		}
		content := gjson.GetBytes(lineBytes, "message.content")
		if content.IsArray() {
			content = content.Get("0.text")
		}
		body = content.Str
	default:
		return false
	}
	body, ok := strings.CutPrefix(strings.TrimSpace(body), "<task-notification>")
	return ok && strings.HasPrefix(
		strings.TrimSpace(body), string(claudeForkSourceMarker),
	)
}

// claudeForkChildPrefix returns the child session id prefix at the end of
// a /fork stdout line, such as "session waiting for a prompt · <name> ·
// 1a2b3c4d". It returns "" for any other shape.
func claudeForkChildPrefix(lineBytes []byte) string {
	if gjson.GetBytes(lineBytes, "subtype").Str != "local_command" ||
		gjson.GetBytes(lineBytes, "commandRun.command").Str != "fork" {
		return ""
	}
	content := gjson.GetBytes(lineBytes, "content").Str
	content = strings.TrimSuffix(strings.TrimSpace(content), "</local-command-stdout>")
	i := strings.LastIndex(content, "·")
	if i < 0 {
		return ""
	}
	prefix := strings.TrimSpace(content[i+len("·"):])
	if len(prefix) < 8 || !IsValidSessionID(prefix) {
		return ""
	}
	return prefix
}

// claudeScanUUIDs streams the uuid of every uuid-bearing line of a
// transcript in order and returns the transcript's lineage marks. Errors and malformed
// lines are skipped; lineage resolution fails open on incomplete data.
func claudeScanUUIDs(
	ctx context.Context, path string, visit func(uuid string),
) (claudeLineageMarks, bool, error) {
	var marks claudeLineageMarks
	f, err := os.Open(path)
	if err != nil {
		return marks, false, nil
	}
	defer f.Close()
	lr := newLineReaderContext(ctx, f, maxLineSize)
	defer releaseLineReader(lr)
	uuidLines := 0
	interrupted := false
	for {
		if err := ctx.Err(); err != nil {
			return marks, false, err
		}
		lineBytes, ok := lr.nextBytes()
		if !ok {
			break
		}
		if !gjson.ValidBytes(lineBytes) {
			continue
		}
		uuid := gjson.GetBytes(lineBytes, "uuid").Str
		entryType := gjson.GetBytes(lineBytes, "type").Str
		if claudeIsForkSourceNotice(lineBytes, entryType) {
			marks.forkSourceAt = append(marks.forkSourceAt, uuidLines)
		}
		if uuid == "" {
			if uuidLines > 0 && !interrupted {
				interrupted = true
				marks.contiguous = uuidLines
			}
			if entryType == "continued-in" {
				if id := gjson.GetBytes(lineBytes, "continuedInSessionId").Str; id != "" {
					marks.continuedIn = append(marks.continuedIn, strings.Clone(id))
				}
			}
			continue
		}
		if entryType == "system" {
			if prefix := claudeForkChildPrefix(lineBytes); prefix != "" {
				marks.forkChildPrefixes = append(
					marks.forkChildPrefixes, strings.Clone(prefix),
				)
			}
		}
		uuidLines++
		visit(uuid)
	}
	if !interrupted {
		marks.contiguous = uuidLines
	}
	return marks, lr.Err() == nil, nil
}

// claudeLineageCaptureSiblings returns the sibling transcripts the lineage
// parser must be able to read for path: when path is a top-level project
// transcript whose head is a bg-marked fork, every sibling .jsonl in the same
// project directory whose first chain entry replays the fork's root uuid.
// Only bg-marked forks ever trim or link, so interactive originals, subagent
// transcripts, and unrelated conversations contribute no entries. The set is
// exactly the candidate filter claudeResolveSiblingLineage applies, so a
// capture generation carries every input the parser needs at replay time.
func claudeLineageCaptureSiblings(ctx context.Context, path string) ([]string, error) {
	self, err := claudeSniffHead(ctx, path)
	if err != nil {
		return nil, err
	}
	if !self.ok || !self.rootIsBG {
		return nil, nil
	}
	dir := filepath.Dir(path)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, invalidRawCapturePlan(
			"read Claude lineage siblings: %s", rawCaptureFilesystemError(err),
		)
	}
	base := filepath.Base(path)
	var siblings []string
	for _, entry := range dirEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := entry.Name()
		if entry.IsDir() || name == base ||
			!strings.HasSuffix(name, ".jsonl") ||
			strings.HasPrefix(name, "agent-") {
			continue
		}
		siblingPath := filepath.Join(dir, name)
		sniff, err := claudeSniffHead(ctx, siblingPath)
		if err != nil {
			return nil, err
		}
		if sniff.ok && sniff.rootUUID == self.rootUUID {
			siblings = append(siblings, siblingPath)
		}
	}
	return siblings, nil
}

// claudeResolveSiblingLineage establishes the background-fork lineage
// for path, or returns nil when no lineage can be positively oriented.
// Sibling discovery is head-sniff only (memoized per size and mtime,
// with a bounded leading digest verifying every metadata hit).
// Qualifying candidates' full uuid sets are read once per full parse of a
// bg-marked fork.
func claudeResolveSiblingLineage(
	ctx context.Context, path string,
) (*claudeLineagePlan, error) {
	self, err := claudeSniffHead(ctx, path)
	if err != nil {
		return nil, err
	}
	if !self.ok || !self.rootIsBG {
		return nil, nil
	}
	dir := filepath.Dir(path)
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil //nolint:nilerr // Unavailable optional lineage metadata yields no lineage hint.
	}
	base := filepath.Base(path)
	type candidate struct {
		path string
		stem string
		bg   bool
	}
	var candidates []candidate
	for _, de := range dirEntries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := de.Name()
		if de.IsDir() || name == base ||
			!strings.HasSuffix(name, ".jsonl") ||
			strings.HasPrefix(name, "agent-") {
			continue
		}
		sibling, err := claudeSniffHead(ctx, filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		if !sibling.ok || sibling.rootUUID != self.rootUUID {
			continue
		}
		candidates = append(candidates, candidate{
			path: filepath.Join(dir, name),
			stem: strings.TrimSuffix(name, ".jsonl"),
			bg:   sibling.rootIsBG,
		})
	}
	if len(candidates) == 0 {
		return nil, nil
	}
	// Non-bg candidates first so an interactive original wins a run
	// tie against an unrelated bg fork of the same original.
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].bg != candidates[j].bg {
			return !candidates[i].bg
		}
		return candidates[i].stem < candidates[j].stem
	})

	var forkSeq []string
	selfMarks, complete, err := claudeScanUUIDs(ctx, path, func(uuid string) {
		forkSeq = append(forkSeq, uuid)
	})
	if err != nil {
		return nil, err
	}
	if !complete || len(forkSeq) == 0 {
		return nil, nil
	}

	// Pick the candidate whose uuid set covers the longest contiguous
	// leading run of the fork: with chained ancestors sharing one
	// root, the nearest ancestor is the one the fork replayed. A bg
	// candidate that fully covers the fork is its descendant or twin,
	// where content gives no direction. A strict superset never wins.
	// Equal-set twins elect direction deterministically by stem: the
	// larger stem trims against the smaller, never the reverse, so
	// the smaller twin keeps the content and no cycle can form.
	//
	// Explicit evidence narrows the field first. A session this
	// transcript names as its handoff or /fork child is a descendant, and
	// so is a candidate whose shared run reaches past the fork's leading
	// uninterrupted block, because that run includes lines this
	// transcript wrote itself. A candidate whose continued-in record names
	// this transcript is its parent regardless of run length.
	forkStem := strings.TrimSuffix(base, ".jsonl")
	forkUUIDs := make(map[string]struct{}, len(forkSeq))
	for _, uuid := range forkSeq {
		forkUUIDs[uuid] = struct{}{}
	}
	type scored struct {
		stem  string
		set   map[string]struct{}
		marks claudeLineageMarks
		run   int
		bg    bool
	}
	var best, handedOff *scored
	bestTied := false
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if selfMarks.namesChild(c.stem) {
			continue
		}
		set := make(map[string]struct{})
		marks, complete, err := claudeScanUUIDs(ctx, c.path, func(uuid string) {
			set[uuid] = struct{}{}
		})
		if err != nil {
			return nil, err
		}
		if !complete {
			continue
		}
		run := 0
		for _, uuid := range forkSeq {
			if _, ok := set[uuid]; !ok {
				break
			}
			run++
		}
		if c.bg && run == len(forkSeq) &&
			(len(set) != len(forkUUIDs) || forkStem < c.stem) {
			continue
		}
		cur := &scored{stem: c.stem, set: set, marks: marks, run: run, bg: c.bg}
		if run > 0 && marks.continues(forkStem) {
			if handedOff == nil || run > handedOff.run {
				handedOff = cur
			}
			continue
		}
		if run > selfMarks.contiguous {
			continue
		}
		switch {
		case best == nil || run > best.run:
			if run > 0 {
				best = cur
				bestTied = false
			}
		case run > 0 && run == best.run && c.bg == best.bg && !maps.Equal(set, best.set):
			bestTied = true
		}
	}
	// Different UUID sets sharing a maximum do not identify an ancestor:
	// they may be independent forks. Identical sets keep the smallest stem.
	if handedOff != nil {
		best = handedOff
	} else if best == nil || bestTied {
		return nil, nil
	}
	// A continued-in record makes the transcript a handoff, whatever else
	// it holds.
	relationship := RelContinuation
	if !best.marks.continues(forkStem) &&
		(selfMarks.forkSourceAfter(best.run) || best.marks.namesChild(forkStem)) {
		relationship = RelFork
	}
	return &claudeLineagePlan{
		parentSessionID: best.stem,
		relationship:    relationship,
		replayCount:     best.run,
	}, nil
}
