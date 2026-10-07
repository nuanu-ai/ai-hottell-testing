package clickhouse

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"git.alva.dev/alva/harness-telemetry/internal/domain/telemetry"
)

// transcriptsService is the service.name of the records that hold transcript lines.
const transcriptsService = "hottell-transcripts"

var (
	errNoTranscriptFile = errors.New("read transcript lines: the key names no file")
	errNoTranscriptUser = errors.New("read transcript lines: the key names no user")
)

// Expressions over otel_logs. The server keeps the first record of a line key and drops the
// repeats, so the table holds repeats and the reads collapse them. A transcript line's Timestamp
// is its own time or, when the line has none, the moment it was read; it is never 0 and never
// the order of the lines, which the transcript.line attribute alone gives.
const (
	userExpr = "ResourceAttributes['hottell.user.id']"
	// lineExpr is the number of the line in its file; a record without a number reads as 0 and
	// is left out by the queries.
	lineExpr = "toUInt64OrZero(LogAttributes['transcript.line'])"
	// fileExpr is the last segment of transcript.path without ".zst": Codex moves a rollout
	// between directories and compresses old ones, and the name stays.
	fileExpr = "replaceRegexpOne(arrayElement(splitByChar('/', LogAttributes['transcript.path']), -1), '\\\\.zst$', '')"
	// kindExpr is the kind of the file as the record states it.
	kindExpr = "LogAttributes['transcript.kind']"
	// wellFormed leaves out the records the reads cannot hand back as the domain describes them:
	// a record without a line number, without a file name, or with a kind that is neither main
	// nor subagent. The hottell contract makes the path and the kind required, so such a record
	// is damaged and would otherwise be listed as a file that cannot be read, or carry a kind
	// the domain does not know.
	wellFormed = lineExpr + " > 0 AND " + fileExpr + " != '' AND " + kindExpr + " IN ('main', 'subagent')"
)

// TranscriptLines returns the lines of the file key names, each line once, ordered by line
// number. The key of a line is (user, agent, session, file, line); of the repeats of a key the
// earliest stored one is returned. fromLine and toLine bound the line numbers, both included,
// and 0 leaves an end open. A key without a file or without a user is an error that is not
// telemetry.ErrUnavailable: a session's files have lines with the same numbers, and so do the
// files of two people with the same session id, and mixing them would hand back a transcript
// that never existed. The files of every person are enumerated by TranscriptFiles.
func (r *Reader) TranscriptLines(
	ctx context.Context, key telemetry.TranscriptKey, fromLine, toLine uint64,
) (telemetry.Lines, error) {
	return r.transcriptLines(ctx, key, fromLine, toLine, "", "Body")
}

// codexFactRecords keeps the records of a rollout the Codex facts of the domain read: the
// boundaries of a turn, its token counts and finished items among event_msg, the calls and their
// outputs among response_item, turn_context and token_usage_record. The messages and the
// reasoning are left in the store.
const codexFactRecords = `(JSONExtractString(Body, 'type') IN ('turn_context', 'token_usage_record')
  OR (JSONExtractString(Body, 'type') = 'event_msg' AND JSONExtractString(Body, 'payload', 'type')
    IN ('task_started', 'task_complete', 'turn_aborted', 'token_count', 'item_completed'))
  OR (JSONExtractString(Body, 'type') = 'response_item' AND JSONExtractString(Body, 'payload', 'type')
    IN ('function_call', 'custom_tool_call', 'local_shell_call', 'function_call_output', 'custom_tool_call_output')))`

// codexOutputHead is the most characters of an output's text CodexFactLines hands back: twice
// the head the domain searches for an exit code when the text has no "Output:" line. A header
// that runs past it, which Codex never writes, is searched within the first outputHeaderMax
// bytes of the domain, as on the stored line when no "Output:" line comes within them. The cut
// counts characters, not bytes, and toJSONString writes a character in at most six bytes (a
// control character as \u00XX): a reduced line of a printable ASCII head stays under 8 KiB, of any head
// under 6 × 4096 bytes (24 KiB) plus its envelope (HT-387).
const codexOutputHead = 4096

// pick is if(cond, then, otherwise) evaluated without short-circuit: both branches are computed
// for every row and the condition chooses. ClickHouse evaluates a branch of if, multiIf, and or
// or lazily by filtering its arguments to the rows that reach it, which copies Body once for
// every branch and every nested one; over the rollouts of a period those copies ran a single
// read past 5 GiB, where the same expression computed eagerly stays under 500 MiB (HT-457). The
// reader's profile is readonly and cannot turn short_circuit_function_evaluation off, so the
// expressions of the body choose with pick and join conditions with every, and no branch reads
// Body lazily. Every function under them returns a value for any row and never throws.
func pick(cond, then, otherwise string) string {
	return "arrayElement([" + otherwise + ", " + then + "], toUInt8(" + cond + ") + 1)"
}

// every is the conjunction of conds evaluated without short-circuit, as pick says.
func every(conds ...string) string {
	parts := make([]string, len(conds))
	for i, c := range conds {
		parts[i] = "toUInt8(" + c + ")"
	}
	return "(" + strings.Join(parts, " * ") + ")"
}

// Parts of codexFactBody. A JSONExtract call with a path parses the whole Body, and ClickHouse
// computes an expression once however often a query names it, so the body names as few distinct
// paths as it can: about thirty parses of every record made the reduction most of the time of the
// Codex period read (HT-493). The output is read raw once and its parts taken from that text; the
// fields of the item come from one map of their raw texts; a payload field is read raw once and
// typed from that text. A raw text reads as its path read it: the same text by JSONExtractRaw, the
// same string by JSONExtractString, the same JSONType but that of a number, which may read Int64
// where the path read Double and stays a number. A map of the record's or the payload's fields
// would serialize the whole payload or item again, which costs more than the parses it saves.
const (
	codexType        = "JSONExtractString(Body, 'type')"
	codexPayloadType = "JSONExtractString(Body, 'payload', 'type')"
	codexOutput      = "JSONExtractRaw(Body, 'payload', 'output')"
	codexOutputType  = "JSONType(" + codexOutput + ")"
	// codexItemFields maps the fields of the payload's item to their raw texts, empty when the
	// item is absent or not an object; of a repeated key the first value counts, as a path takes it.
	codexItemFields = "CAST(JSONExtractKeysAndValuesRaw(Body, 'payload', 'item'), 'Map(String, String)')"
	// codexSpace is one character of unicode.IsSpace: the ASCII white space, U+0085, U+00A0,
	// U+1680, U+2000 to U+200A, U+2028, U+2029, U+202F, U+205F and U+3000.
	codexSpace = "[\\t\\n\\v\\f\\r ]|\u0085|\u00a0|\u1680|\u2000|\u2001|\u2002|\u2003|\u2004|\u2005|\u2006|" +
		"\u2007|\u2008|\u2009|\u200a|\u2028|\u2029|\u202f|\u205f|\u3000"
	// codexNumberTypes is the condition that a JSON value is a number, after its JSONType.
	codexNumberTypes = " IN ('Int64', 'UInt64', 'Double')"
)

// codexPayload is the raw JSON text of the field key of a Codex record's payload, empty when absent.
func codexPayload(key string) string { return "JSONExtractRaw(Body, 'payload', '" + key + "')" }

// codexItem is the raw JSON text of the field key of the payload's item, empty when absent.
func codexItem(key string) string { return codexItemFields + "['" + key + "']" }

// codexOutputRaw is the one text of an output the domain reads: a string as it is, of an array
// the first text that is a non-empty string; a text of another type counts as none, as the
// domain skips it, where JSONExtractString would render a number as a string.
func codexOutputRaw() string {
	return pick(codexOutputType+" = 'String'", "JSONExtractString("+codexOutput+")",
		pick(codexOutputType+" = 'Array'", "arrayFirst(x -> x != '', arrayMap("+
			"x -> if(JSONType(x, 'text') = 'String', JSONExtractString(x, 'text'), ''), "+
			"JSONExtractArrayRaw("+codexOutput+")))", "''"))
}

// codexOutputTrimmed is that text without white space at either end, as the domain's
// strings.TrimSpace trims it before it tries the text as a JSON object: the space of
// unicode.IsSpace, written as alternatives of whole characters so that the pattern matches UTF-8
// bytes whether re2 reads it as UTF-8 or as Latin-1.
func codexOutputTrimmed() string {
	return "replaceRegexpAll(" + codexOutputRaw() + ", '^(?:" + codexSpace + ")+|(?:" + codexSpace + ")+$', '')"
}

// codexMetadataOf is the object {"metadata": {"exit_code": E, "duration_seconds": D}} of the JSON
// object obj: of its metadata only the two numbers the domain reads leave, each null unless it
// is a number, and the metadata is null unless it is an object.
func codexMetadataOf(obj string) string {
	number := func(key string) string {
		return pick("JSONType("+obj+", 'metadata', '"+key+"')"+codexNumberTypes,
			"JSONExtractRaw("+obj+", 'metadata', '"+key+"')", "'null'")
	}
	return "concat(char(123), '\"metadata\":', " + pick("JSONType("+obj+", 'metadata') = 'Object'",
		"concat(char(123), '\"exit_code\":', "+number("exit_code")+
			", ',\"duration_seconds\":', "+number("duration_seconds")+", char(125))", "'null'") + ", char(125))"
}

// codexFactBody returns the body CodexFactLines hands back. An output record of a call is rebuilt
// from what the domain reads of it, so that no output leaves ClickHouse whole: its type, its
// call_id and, of the output, the exit code and duration of the metadata of an object or of a
// text holding a JSON object, else the head of the text (of an array, its first non-empty
// text). An item_completed record is rebuilt the same way, as codexItemCompletedBody says: Codex
// writes into it the call's whole output, and those bodies, kept whole, made the period read of
// HT-457 exceed the memory of ClickHouse. Every other record is rebuilt as codexRecordBody says,
// so that no record leaves ClickHouse whole (HT-536).
//
// Two shapes the domain reads otherwise are accepted, as serde_json, which writes the rollout,
// never writes them (HT-374). A key repeated in an object counts by its first value here, where
// Go's encoding/json keeps the last: a repeated output, text, metadata, exit_code or
// duration_seconds may give another exit code or duration. And a text over codexOutputHead
// characters holding a JSON object nested past what isValidJSON accepts, about 1500 levels
// where Go accepts 10000, goes as its head, so its exit code is lost. Each is a fact that
// differs, never more of the output leaving ClickHouse. The braces are written char(123) and
// char(125): the driver takes a query holding "{…:…}" for one with server-side parameters and
// refuses the positional arguments. The expression chooses with pick, as pick says.
func codexFactBody() string {
	trimmed := codexOutputTrimmed()
	textual := codexOutputType + " IN ('String', 'Array')"
	output := pick(codexOutputType+" = 'Object'", codexMetadataOf(codexOutput),
		pick(every(textual, "startsWith("+trimmed+", char(123))", "isValidJSON("+trimmed+")"), codexMetadataOf(trimmed),
			pick(textual, "toJSONString(substringUTF8("+codexOutputRaw()+", 1, "+strconv.Itoa(codexOutputHead)+"))", "'null'")))
	reducedOutput := `concat(char(123), '"type":"response_item","payload":', char(123), '"type":', toJSONString(` + codexPayloadType + `),
    ',"call_id":', toJSONString(JSONExtractString(Body, 'payload', 'call_id')), ',"output":', ` + output + `,
    char(125), char(125))`
	return pick(every(codexType+" = 'event_msg'", codexPayloadType+" = 'item_completed'"), codexItemCompletedBody(),
		pick(every(codexType+" = 'response_item'", codexPayloadType+" IN ('function_call_output', 'custom_tool_call_output')"),
			reducedOutput, codexRecordBody()))
}

// codexRecordBody returns a fact record other than an output or an item_completed rebuilt from
// the fields the domain reads of it: the record's type, and of the payload its type, turn_id,
// call_id, the model as codexModelAt says, response_id, duration_ms, the token usages usage and turn_token_usage, and of
// info total_token_usage and last_token_usage. The arguments of a call, the instructions of a
// turn_context and the message of a task_complete stay in the store (HT-536).
//
// A field reads as the domain's decoder reads it from the stored line. A string or a number is
// written as its own JSON value and any other value as null, which the decoder takes as the empty
// value a field of the wrong type leaves. A token usage is the object of its six counts; a
// usage that is null or absent is null, any other value that is not an object {}, as the decoder
// allocates the usage before it refuses the value. A value is written as ClickHouse serializes it
// again, not as stored: an escape of a string is written as its character and a number in another
// spelling as ClickHouse reads it, so that 1e3 becomes 1000. A token count stored as 1e3 or 5.0,
// which the decoder refuses as no integer, then reads as a number; a key repeated in an object
// counts by its first value, and a key spelled in another case is not read, where the decoder
// takes the last and matches the case loosely. serde_json, which writes the rollout, writes none
// of these shapes, as codexFactBody says of its own.
func codexRecordBody() string {
	return `concat(char(123), '"type":', toJSONString(` + codexType + `), ',"payload":', char(123),
    '"type":', toJSONString(` + codexPayloadType + `),
    ',"turn_id":', ` + codexStringAt(codexPayload("turn_id")) + `,
    ',"call_id":', ` + codexStringAt(codexPayload("call_id")) + `,
    ',"model":', ` + codexModelAt(codexPayload("model")) + `,
    ',"response_id":', ` + codexStringAt(codexPayload("response_id")) + `,
    ',"duration_ms":', ` + codexNumberAt(codexPayload("duration_ms")) + `,
    ',"usage":', ` + codexTokensAt(codexPayload("usage")) + `,
    ',"turn_token_usage":', ` + codexTokensAt(codexPayload("turn_token_usage")) + `,
    ',"info":', ` + codexInfoAt(codexPayload("info")) + `,
    char(125), char(125))`
}

// codexModelAt is the model of the JSON value raw as the domain's modelToken keeps it: a short
// identifier as it is, an empty string empty, and any other string as " ", which is not empty,
// so that it takes the place of an earlier model as the stored one does, and is no identifier,
// so that the domain leaves the turn's model empty as it does for the stored one. No free text
// of the rollout leaves ClickHouse as a model; a value that is not a string is null.
func codexModelAt(raw string) string {
	model := "JSONExtractString(" + raw + ")"
	return pick("JSONType("+raw+") = 'String'",
		pick("match("+model+", '^[A-Za-z0-9._:/@-]{1,128}$')", "toJSONString("+model+")",
			pick(model+" = ''", `'""'`, `'" "'`)), "'null'")
}

// codexStructAt is the JSON value raw, a field's raw text, where it is not an object, as the
// domain's decoder reads it into a pointer to a struct: null when it is null or absent, else {}.
func codexStructAt(raw string) string {
	return pick("JSONType("+raw+") = 'Null'", "'null'", "concat(char(123), char(125))")
}

// codexTokensAt is the token usage of the JSON value raw: of an object its six counts, each
// null unless it is a number; any other value as codexStructAt says.
func codexTokensAt(raw string) string {
	keys := []string{
		"input_tokens", "cached_input_tokens", "cache_write_input_tokens", "output_tokens",
		"reasoning_output_tokens", "total_tokens",
	}
	parts := make([]string, len(keys))
	for i, k := range keys {
		sep := ","
		if i == 0 {
			sep = ""
		}
		parts[i] = "'" + sep + `"` + k + `":', ` + codexNumberAt("JSONExtractRaw("+raw+", '"+k+"')")
	}
	return pick("JSONType("+raw+") = 'Object'",
		"concat(char(123), "+strings.Join(parts, ", ")+", char(125))", codexStructAt(raw))
}

// codexInfoAt is the info of a token_count of the JSON value raw: of an object its
// total_token_usage and last_token_usage as codexTokensAt says; any other value as codexStructAt
// says.
func codexInfoAt(raw string) string {
	return pick("JSONType("+raw+") = 'Object'",
		`concat(char(123), '"total_token_usage":', `+codexTokensAt("JSONExtractRaw("+raw+", 'total_token_usage')")+
			`, ',"last_token_usage":', `+codexTokensAt("JSONExtractRaw("+raw+", 'last_token_usage')")+`, char(125))`,
		codexStructAt(raw))
}

// codexNumberAt is the JSON value raw, a field's raw text, null unless it is a number, as the
// domain leaves a field of another type empty.
func codexNumberAt(raw string) string {
	return pick("JSONType("+raw+")"+codexNumberTypes, raw, "'null'")
}

// codexStringAt is the string of the JSON value raw as JSON, null unless it is a string.
func codexStringAt(raw string) string {
	return pick("JSONType("+raw+") = 'String'", "toJSONString(JSONExtractString("+raw+"))", "'null'")
}

// codexStatusAt is codexStringAt kept only when the domain's statusToken keeps it, a short
// identifier, so that no free text of the rollout leaves ClickHouse.
func codexStatusAt(raw string) string {
	return pick(every("JSONType("+raw+") = 'String'", "match(JSONExtractString("+raw+"), '^[A-Za-z0-9_-]{1,64}$')"),
		"toJSONString(JSONExtractString("+raw+"))", "'null'")
}

// codexItemCompletedBody returns the item_completed record rebuilt from what the domain reads of
// it: the turn id, started_at_ms and completed_at_ms of the payload, and of its item the id,
// status, exit code, the Rust duration {secs, nanos} and durationMs. The output, the command and
// every other field of the item stay in the store (HT-457).
func codexItemCompletedBody() string {
	duration := codexItem("duration")
	durationObj := pick("JSONType("+duration+") = 'Object'", `concat(char(123),
        '"secs":', `+codexNumberAt("JSONExtractRaw("+duration+", 'secs')")+`,
        ',"nanos":', `+codexNumberAt("JSONExtractRaw("+duration+", 'nanos')")+`, char(125))`, "'null'")
	item := pick("JSONType(Body, 'payload', 'item') = 'Object'", `concat(char(123),
      '"id":', `+codexStringAt(codexItem("id"))+`,
      ',"status":', `+codexStatusAt(codexItem("status"))+`,
      ',"exit_code":', `+codexNumberAt(codexItem("exit_code"))+`,
      ',"duration":', `+durationObj+`,
      ',"durationMs":', `+codexNumberAt(codexItem("durationMs"))+`,
      char(125))`, "'null'")
	return `concat(char(123), '"type":"event_msg","payload":', char(123), '"type":"item_completed"',
    ',"turn_id":', ` + codexStringAt(codexPayload("turn_id")) + `,
    ',"started_at_ms":', ` + codexNumberAt(codexPayload("started_at_ms")) + `,
    ',"completed_at_ms":', ` + codexNumberAt(codexPayload("completed_at_ms")) + `,
    ',"item":', ` + item + `,
    char(125), char(125))`
}

// CodexFactLines returns the lines of the rollout key names that the Codex facts read, ordered
// by line number and each once, as TranscriptLines does. The filter on the record's type runs in
// ClickHouse, so the other records never leave it, and an output record comes back reduced, as
// codexFactBody says, not as stored.
func (r *Reader) CodexFactLines(ctx context.Context, key telemetry.TranscriptKey) (telemetry.Lines, error) {
	return r.transcriptLines(ctx, key, 0, 0, codexFactRecords, codexFactBody())
}

// CodexFactsInPeriod returns, in one read over every Codex session of the period, what
// TranscriptFiles and CodexFactLines return of each: the files, with the kind of the first line
// and the highest line, and the lines the Codex facts read, reduced as codexFactBody says, each
// (user, session, file, line) once with its session (HT-457). Only the records stored in
// [f.From, f.To) are read, so a file's kind and highest line are those of its records in the
// period. Both are ordered by session, file and user, the lines then by line. f.UserID uuid.Nil
// reads every person's; f.Agent and f.SessionID are not read. telemetry.ErrNoHookPeriod without a
// period.
func (r *Reader) CodexFactsInPeriod(
	ctx context.Context, f telemetry.Filter,
) ([]telemetry.SessionFile, []telemetry.SessionLine, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return nil, nil, telemetry.ErrNoHookPeriod
	}
	where := "WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND Timestamp >= ? AND Timestamp < ? AND " + wellFormed
	args := []any{transcriptsService, "codex", f.From, f.To}
	if f.UserID != uuid.Nil {
		where += " AND " + userExpr + " = ?"
		args = append(args, f.UserID.String())
	}
	// A row with line 0 is a file: its kind and highest line; any other is a fact line. A stored
	// line is never 0, which wellFormed leaves out. The rows stream unordered and with the
	// repeats of a line, which firstOfEachLine drops, so ClickHouse holds no fact line.
	query := "SELECT uid, sid, file, line, kind, body, at, maxLine FROM (" +
		"SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		"toUInt64(0) AS line, argMin(" + kindExpr + ", (" + lineExpr + ", Timestamp)) AS kind, '' AS body, " +
		"min(Timestamp) AS at, max(" + lineExpr + ") AS maxLine " +
		"FROM otel.otel_logs " + where + " GROUP BY uid, sid, file " +
		"UNION ALL " +
		"SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		lineExpr + " AS line, " + kindExpr + " AS kind, " + codexFactBody() + " AS body, Timestamp AS at, " +
		"toUInt64(0) AS maxLine " +
		"FROM otel.otel_logs " + where + " AND " + codexFactRecords + ")"
	queryArgs := slices.Concat(args, args)
	if r.aggregatesReady(ctx) {
		// The aggregates (aggregates.go): the same rows, without parsing a record of otel_logs.
		awhere := "WHERE at >= ? AND at < ?"
		fwhere := "WHERE minute >= toStartOfMinute(?) AND minute < ?"
		aargs := []any{f.From, f.To}
		if f.UserID != uuid.Nil {
			awhere += " AND uid = ?"
			fwhere += " AND uid = ?"
			aargs = append(aargs, f.UserID.String())
		}
		query = "SELECT uid, sid, file, toUInt64(0) AS line, argMinMerge(kind) AS kind, '' AS body, " +
			"minMerge(at) AS at, maxMerge(maxLine) AS maxLine FROM otel.ht_codex_files " + fwhere + " GROUP BY uid, sid, file " +
			"UNION ALL SELECT uid, sid, file, line, kind, body, at, toUInt64(0) AS maxLine FROM otel.ht_codex_fact_lines " + awhere
		queryArgs = append(slices.Clone(aargs), aargs...)
	}
	rows, err := r.conn.Query(ctx, query, queryArgs...)
	if err != nil {
		return nil, nil, r.unavailable("read codex facts of the period", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		files []telemetry.SessionFile
		lines []telemetry.SessionLine
	)
	for rows.Next() {
		var (
			user, sid, file, kind, body string
			line, maxLine               uint64
			at                          time.Time
		)
		if err := rows.Scan(&user, &sid, &file, &line, &kind, &body, &at, &maxLine); err != nil {
			return nil, nil, r.unavailable("scan codex facts of the period", err)
		}
		uid, err := uuid.Parse(user)
		if err != nil {
			return nil, nil, fmt.Errorf("read codex facts of the period: stored user id is not a uuid: %w", err)
		}
		if line == 0 {
			files = append(files, telemetry.SessionFile{
				SessionID: sid, TranscriptFile: telemetry.TranscriptFile{UserID: uid, Name: file, Kind: kind, MaxLine: maxLine},
			})
			continue
		}
		lines = append(lines, telemetry.SessionLine{
			UserID: uid, SessionID: sid, File: file,
			TranscriptLine: telemetry.TranscriptLine{Number: line, Kind: kind, Body: body, Time: at},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, nil, r.unavailable("read codex facts of the period", err)
	}
	slices.SortFunc(files, func(a, b telemetry.SessionFile) int {
		return cmp.Or(cmp.Compare(a.SessionID, b.SessionID), cmp.Compare(a.Name, b.Name), bytes.Compare(a.UserID[:], b.UserID[:]))
	})
	return files, firstOfEachLine(lines), nil
}

// firstOfEachLine orders lines by session, file, user and line and keeps, of the repeats of a
// line, the earliest stored one, as LIMIT 1 BY kept it before HT-457: done in ClickHouse, the
// sort carried every fact record's whole Body, as the planner computes the reduced body after
// it, and ran the period read out of memory.
func firstOfEachLine(lines []telemetry.SessionLine) []telemetry.SessionLine {
	slices.SortStableFunc(lines, func(a, b telemetry.SessionLine) int {
		return cmp.Or(cmp.Compare(a.SessionID, b.SessionID), cmp.Compare(a.File, b.File),
			bytes.Compare(a.UserID[:], b.UserID[:]), cmp.Compare(a.Number, b.Number), a.Time.Compare(b.Time))
	})
	return slices.CompactFunc(lines, func(a, b telemetry.SessionLine) bool {
		return a.SessionID == b.SessionID && a.File == b.File && a.UserID == b.UserID && a.Number == b.Number
	})
}

// claudeUsageRecords keeps the records of a Claude transcript that carry a model response's
// tokens: the assistant records with a usage object.
const claudeUsageRecords = `JSONExtractString(Body, 'type') = 'assistant' AND JSONType(Body, 'message', 'usage') = 'Object'`

// claudeUsageInt is a token count of the usage, as a JSON number; 0 when it is absent.
const claudeUsageInt = "toString(JSONExtractInt(Body, 'message', 'usage', '%s'))"

// claudeCacheTTLInt is the SQL of one count of the split of the cache write by TTL in
// message.usage.cache_creation, 0 when the line has none (HT-517).
const claudeCacheTTLInt = "toString(JSONExtractInt(Body, 'message', 'usage', 'cache_creation', '%s'))"

// claudeUsageBody returns the body ClaudeUsageLines hands back: the record rebuilt from what the
// tokens are counted by, the request id, time, side-chain flag, message id and model, four
// token counts of the usage and the split of the cache write by TTL, so that no message content leaves ClickHouse. The braces are
// written char(123) and char(125), as in codexFactBody.
func claudeUsageBody() string {
	return `concat(char(123), '"type":"assistant","requestId":', toJSONString(JSONExtractString(Body, 'requestId')),
  ',"timestamp":', toJSONString(JSONExtractString(Body, 'timestamp')),
  ',"isSidechain":', if(JSONExtractBool(Body, 'isSidechain'), 'true', 'false'),
  ',"message":', char(123), '"id":', toJSONString(JSONExtractString(Body, 'message', 'id')),
  ',"model":', toJSONString(JSONExtractString(Body, 'message', 'model')),
  ',"usage":', char(123), '"input_tokens":', ` + fmt.Sprintf(claudeUsageInt, "input_tokens") + `,
  ',"cache_read_input_tokens":', ` + fmt.Sprintf(claudeUsageInt, "cache_read_input_tokens") + `,
  ',"cache_creation_input_tokens":', ` + fmt.Sprintf(claudeUsageInt, "cache_creation_input_tokens") + `,
  ',"cache_creation":', char(123), '"ephemeral_5m_input_tokens":', ` + fmt.Sprintf(claudeCacheTTLInt, "ephemeral_5m_input_tokens") + `,
  ',"ephemeral_1h_input_tokens":', ` + fmt.Sprintf(claudeCacheTTLInt, "ephemeral_1h_input_tokens") + `, char(125),
  ',"output_tokens":', ` + fmt.Sprintf(claudeUsageInt, "output_tokens") + `,
  char(125), char(125), char(125))`
}

// ClaudeUsageLines returns the lines of a Claude transcript that carry a model response's tokens,
// ordered by line number and each once, as TranscriptLines does. The filter runs in ClickHouse,
// and a line comes back reduced, as claudeUsageBody says, not as stored.
func (r *Reader) ClaudeUsageLines(ctx context.Context, key telemetry.TranscriptKey) (telemetry.Lines, error) {
	return r.transcriptLines(ctx, key, 0, 0, claudeUsageRecords, claudeUsageBody())
}

// ClaudeSessionUsage returns the lines of every transcript file of a Claude session that carry a
// model response's tokens, reduced as ClaudeUsageLines reduces them, in one read: ordered by file,
// user and line, each (user, file, line) once (HT-389). since, when not zero, is the earliest
// stored time read, so that the partitions by day before the session are skipped; userID
// uuid.Nil reads every person's.
func (r *Reader) ClaudeSessionUsage(
	ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
) ([]telemetry.SessionLine, error) {
	var q strings.Builder
	args := make([]any, 0, 5)
	q.WriteString("SELECT " + userExpr + " AS uid, " + fileExpr + " AS file, " + lineExpr + " AS line, " +
		kindExpr + ", " + claudeUsageBody() + ", Timestamp " +
		"FROM otel.otel_logs " +
		"WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND LogAttributes['session.id'] = ? " +
		"AND " + wellFormed + " AND " + claudeUsageRecords)
	args = append(args, transcriptsService, "claude", sessionID)
	if !since.IsZero() {
		q.WriteString(" AND Timestamp >= ?")
		args = append(args, since)
	}
	if userID != uuid.Nil {
		q.WriteString(" AND uid = ?")
		args = append(args, userID.String())
	}
	q.WriteString(" ORDER BY file, uid, line, Timestamp LIMIT 1 BY (uid, file, line)")

	rows, err := r.conn.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, r.unavailable("read claude session usage", err)
	}
	defer func() { _ = rows.Close() }()

	var out []telemetry.SessionLine
	for rows.Next() {
		var l telemetry.SessionLine
		var user string
		if err := rows.Scan(&user, &l.File, &l.Number, &l.Kind, &l.Body, &l.Time); err != nil {
			return nil, r.unavailable("scan claude session usage", err)
		}
		if l.UserID, err = uuid.Parse(user); err != nil {
			return nil, fmt.Errorf("read claude session usage: stored user id is not a uuid: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable("read claude session usage", err)
	}
	return out, nil
}

// ClaudeUsageInPeriod returns the lines of every transcript file of every Claude session that
// carry a model response's tokens and were stored in [f.From, f.To), reduced as ClaudeUsageLines
// reduces them, in one read: ordered by session, file, user and line, each (user, session, file,
// line) once, with its session (HT-453). f.UserID uuid.Nil reads every person's; f.Agent and
// f.SessionID are not read. telemetry.ErrNoHookPeriod without a period.
func (r *Reader) ClaudeUsageInPeriod(ctx context.Context, f telemetry.Filter) ([]telemetry.SessionLine, error) {
	if f.From.IsZero() || f.To.IsZero() || !f.To.After(f.From) {
		return nil, telemetry.ErrNoHookPeriod
	}
	var q strings.Builder
	args := make([]any, 0, 5)
	q.WriteString("SELECT " + userExpr + " AS uid, LogAttributes['session.id'] AS sid, " + fileExpr + " AS file, " +
		lineExpr + " AS line, " + kindExpr + ", " + claudeUsageBody() + ", Timestamp " +
		"FROM otel.otel_logs " +
		"WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND Timestamp >= ? AND Timestamp < ? " +
		"AND " + wellFormed + " AND " + claudeUsageRecords)
	args = append(args, transcriptsService, "claude", f.From, f.To)
	if f.UserID != uuid.Nil {
		q.WriteString(" AND uid = ?")
		args = append(args, f.UserID.String())
	}
	// The rows stream unordered and with the repeats of a line, which firstOfEachLine drops:
	// ORDER BY … LIMIT 1 BY sorted every usage record's whole Body, as ClickHouse computes the
	// reduced body after the sort, and took most of the read's time and memory (HT-493).

	query := q.String()
	if r.aggregatesReady(ctx) {
		// The aggregates (aggregates.go): the same rows, without parsing a record of otel_logs.
		query = "SELECT uid, sid, file, line, kind, body, at FROM otel.ht_claude_usage_lines WHERE at >= ? AND at < ?"
		args = []any{f.From, f.To}
		if f.UserID != uuid.Nil {
			query += " AND uid = ?"
			args = append(args, f.UserID.String())
		}
	}
	rows, err := r.conn.Query(ctx, query, args...)
	if err != nil {
		return nil, r.unavailable("read claude usage of the period", err)
	}
	defer func() { _ = rows.Close() }()

	var out []telemetry.SessionLine
	for rows.Next() {
		var l telemetry.SessionLine
		var user string
		if err := rows.Scan(&user, &l.SessionID, &l.File, &l.Number, &l.Kind, &l.Body, &l.Time); err != nil {
			return nil, r.unavailable("scan claude usage of the period", err)
		}
		if l.UserID, err = uuid.Parse(user); err != nil {
			return nil, fmt.Errorf("read claude usage of the period: stored user id is not a uuid: %w", err)
		}
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable("read claude usage of the period", err)
	}
	return firstOfEachLine(out), nil
}

// Parts of ClaudeSourceRefs: the items of a record's message.content, the ids of its tool_use
// items, and whether one of them is a tool's result. A content that is a string has no items.
const (
	claudeContentItems = "JSONExtractArrayRaw(Body, 'message', 'content')"
	claudeToolUseIDs   = "arrayFilter(x -> x != '', arrayMap(x -> if(JSONExtractString(x, 'type') = 'tool_use', " +
		"JSONExtractString(x, 'id'), ''), " + claudeContentItems + "))"
	claudeHasToolResult = "arrayExists(x -> JSONExtractString(x, 'type') = 'tool_result', " + claudeContentItems + ")"
	// claudeSourceRecords keeps the assistant records with a tool_use and the user records with a
	// promptId that are a prompt, not a tool's result.
	claudeSourceRecords = "((JSONExtractString(Body, 'type') = 'assistant' AND notEmpty(" + claudeToolUseIDs + ")) " +
		"OR (JSONExtractString(Body, 'type') = 'user' AND JSONExtractString(Body, 'promptId') != '' " +
		"AND NOT " + claudeHasToolResult + "))"
)

// ClaudeSourceRefs returns the lines of every transcript file of a Claude session a timeline event
// is built from, as their place and ids: the assistant lines with a tool_use by the items' ids,
// the user lines that are a prompt by their promptId (HT-410). The filter and the ids are taken
// in ClickHouse, so no text leaves it. Ordered by file, user and line, each (user, file, line)
// once; since and userID as in ClaudeSessionUsage.
func (r *Reader) ClaudeSourceRefs(
	ctx context.Context, userID uuid.UUID, sessionID string, since time.Time,
) ([]telemetry.SourceRef, error) {
	var q strings.Builder
	args := make([]any, 0, 5)
	q.WriteString("SELECT " + userExpr + " AS uid, " + fileExpr + " AS file, " + lineExpr + " AS line, " +
		kindExpr + ", if(JSONExtractString(Body, 'type') = 'user', JSONExtractString(Body, 'promptId'), ''), " +
		"if(JSONExtractString(Body, 'type') = 'assistant', " + claudeToolUseIDs + ", []) " +
		"FROM otel.otel_logs " +
		"WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND LogAttributes['session.id'] = ? " +
		"AND " + wellFormed + " AND " + claudeSourceRecords)
	args = append(args, transcriptsService, "claude", sessionID)
	if !since.IsZero() {
		q.WriteString(" AND Timestamp >= ?")
		args = append(args, since)
	}
	if userID != uuid.Nil {
		q.WriteString(" AND uid = ?")
		args = append(args, userID.String())
	}
	q.WriteString(" ORDER BY file, uid, line, Timestamp LIMIT 1 BY (uid, file, line)")

	rows, err := r.conn.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, r.unavailable("read claude source lines", err)
	}
	defer func() { _ = rows.Close() }()

	var out []telemetry.SourceRef
	for rows.Next() {
		var ref telemetry.SourceRef
		var user string
		if err := rows.Scan(&user, &ref.File, &ref.Number, &ref.Kind, &ref.PromptID, &ref.ToolUseIDs); err != nil {
			return nil, r.unavailable("scan claude source lines", err)
		}
		if ref.UserID, err = uuid.Parse(user); err != nil {
			return nil, fmt.Errorf("read claude source lines: stored user id is not a uuid: %w", err)
		}
		if len(ref.ToolUseIDs) == 0 {
			ref.ToolUseIDs = nil
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable("read claude source lines", err)
	}
	return out, nil
}

// transcriptLines is TranscriptLines with where, when not empty, a further condition on the
// records, and body the expression read as a line's Body.
func (r *Reader) transcriptLines(
	ctx context.Context, key telemetry.TranscriptKey, fromLine, toLine uint64, where, body string,
) (telemetry.Lines, error) {
	var lines telemetry.Lines
	err := r.eachTranscriptLine(ctx, key, fromLine, toLine, where, body, func(l telemetry.TranscriptLine) error {
		lines = append(lines, l)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return lines, nil
}

// eachTranscriptLine calls fn with each line transcriptLines would return, in the same order, as
// the rows are read. An error of fn stops the read and is returned as is.
func (r *Reader) eachTranscriptLine(
	ctx context.Context, key telemetry.TranscriptKey, fromLine, toLine uint64, where, body string,
	fn func(telemetry.TranscriptLine) error,
) error {
	if key.File == "" {
		return errNoTranscriptFile
	}
	if key.UserID == uuid.Nil {
		return errNoTranscriptUser
	}

	var q strings.Builder
	args := make([]any, 0, 7)
	q.WriteString("SELECT " + lineExpr + " AS line, " + kindExpr + ", " + body + ", Timestamp " +
		"FROM otel.otel_logs " +
		"WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND LogAttributes['session.id'] = ? " +
		"AND " + fileExpr + " = ? AND " + userExpr + " = ? AND " + wellFormed)
	args = append(args, transcriptsService, key.Agent, key.SessionID, key.File, key.UserID.String())
	if fromLine > 0 {
		q.WriteString(" AND line >= ?")
		args = append(args, fromLine)
	}
	if toLine > 0 {
		q.WriteString(" AND line <= ?")
		args = append(args, toLine)
	}
	if where != "" {
		q.WriteString(" AND " + where)
	}
	q.WriteString(" ORDER BY line, Timestamp LIMIT 1 BY line")

	rows, err := r.conn.Query(ctx, q.String(), args...)
	if err != nil {
		return r.unavailable("read transcript lines", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var l telemetry.TranscriptLine
		var at time.Time
		if err := rows.Scan(&l.Number, &l.Kind, &l.Body, &at); err != nil {
			return r.unavailable("scan transcript line", err)
		}
		l.Time = at
		if err := fn(l); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return r.unavailable("read transcript lines", err)
	}
	return nil
}

// TranscriptFiles returns the transcript files of a session, one per user and file name, ordered
// by name: the kind of the file and the highest line number stored for it. The kind is the one
// of the copy of the file's first line that TranscriptLines returns. userID uuid.Nil reads every
// person's.
func (r *Reader) TranscriptFiles(
	ctx context.Context, userID uuid.UUID, agent, sessionID string,
) ([]telemetry.TranscriptFile, error) {
	var q strings.Builder
	args := make([]any, 0, 4)
	q.WriteString("SELECT " + userExpr + ", " + fileExpr + " AS file, " +
		"argMin(" + kindExpr + ", (" + lineExpr + ", Timestamp)), max(" + lineExpr + ") " +
		"FROM otel.otel_logs " +
		"WHERE ServiceName = ? AND LogAttributes['agent'] = ? AND LogAttributes['session.id'] = ? " +
		"AND " + wellFormed)
	args = append(args, transcriptsService, agent, sessionID)
	if userID != uuid.Nil {
		q.WriteString(" AND " + userExpr + " = ?")
		args = append(args, userID.String())
	}
	q.WriteString(" GROUP BY " + userExpr + ", file ORDER BY file, " + userExpr)

	rows, err := r.conn.Query(ctx, q.String(), args...)
	if err != nil {
		return nil, r.unavailable("read transcript files", err)
	}
	defer func() { _ = rows.Close() }()

	var files []telemetry.TranscriptFile
	for rows.Next() {
		var f telemetry.TranscriptFile
		var user string
		if err := rows.Scan(&user, &f.Name, &f.Kind, &f.MaxLine); err != nil {
			return nil, r.unavailable("scan transcript file", err)
		}
		if f.UserID, err = uuid.Parse(user); err != nil {
			return nil, fmt.Errorf("read transcript files: stored user id is not a uuid: %w", err)
		}
		files = append(files, f)
	}
	if err := rows.Err(); err != nil {
		return nil, r.unavailable("read transcript files", err)
	}
	return files, nil
}
