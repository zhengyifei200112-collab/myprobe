# Report replay identity across retained timestamp spellings

A retained UTC timestamp may spell the same instant with different fractional
widths, such as `12:00:00Z` and `12:00:00.000000000Z`. Comparing the original text
can miss an existing capture and insert a cached report again.

Report ingestion now looks up the existing `captured_sort_ns` key introduced by
migration 041. It keeps the existing node scope, writer reservation, payload
comparison and latest/traffic rules. It does not rewrite retained timestamps,
change migration bytes or the Agent acknowledgement contract. A different
payload at an existing instant is still rejected; a distinct capture one
nanosecond earlier remains valid history without rewinding latest metrics.

`TestReportReplayUsesCaptureInstantAcrossStoredFractionWidths` covers whole
seconds, trailing fractional zeros and a full nanosecond control. It checks
deduplication, preservation of the original history text, conflicting payload
rejection and distinct late capture handling. Existing cached/late-report tests
remain applicable. These are correctness checks, not capacity acceptance.
