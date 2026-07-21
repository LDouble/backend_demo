-- 000010_activity_keyword_fulltext.up.sql
-- Replaces the LIKE '%kw%' pattern with a MySQL FULLTEXT index on (title,
-- summary, location). The original schema used prefix-substring LIKE filters
-- that caused full-table scans beyond a few thousand rows; this index makes
-- the keyword path scale with index_count, not row_count.
--
-- We keep the existing single-column indexes for callers that legitimately
-- want title = 'X' or location LIKE 'prefix%' patterns.

ALTER TABLE activities
    ADD FULLTEXT KEY ft_activity_keyword (title, summary, location) WITH PARSER ngram;
