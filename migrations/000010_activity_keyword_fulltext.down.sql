-- 000010_activity_keyword_fulltext.down.sql
ALTER TABLE activities
    DROP INDEX ft_activity_keyword;
