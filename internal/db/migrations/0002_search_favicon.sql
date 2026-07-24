ALTER TABLE feeds ADD COLUMN favicon_content_type TEXT NOT NULL DEFAULT '';

-- articles_fts indexes article title/author/text plus the owning feed's
-- title, so search covers "title, author, feed name, article text content"
-- per spec. feed_title is a plain (denormalized) column kept in sync via
-- triggers below, since it isn't a column on articles itself. This is a
-- regular content-storing FTS5 table (not "external content"), so it can be
-- kept in sync with plain DELETE/UPDATE/INSERT statements against it -
-- no need for FTS5's special 'delete' command, which requires reproducing a
-- row's exact original indexed values and isn't usable here since a feed's
-- row (and therefore its title) is already gone by the time an AFTER DELETE
-- trigger fires for its cascade-deleted articles.
CREATE VIRTUAL TABLE articles_fts USING fts5(
    title,
    author,
    content_text,
    feed_title
);

CREATE TRIGGER articles_fts_ai AFTER INSERT ON articles BEGIN
    INSERT INTO articles_fts (rowid, title, author, content_text, feed_title)
    VALUES (
        new.id,
        new.title,
        new.author,
        new.content_text,
        (SELECT title FROM feeds WHERE id = new.feed_id)
    );
END;

CREATE TRIGGER articles_fts_ad AFTER DELETE ON articles BEGIN
    DELETE FROM articles_fts WHERE rowid = old.id;
END;

CREATE TRIGGER articles_fts_au AFTER UPDATE ON articles BEGIN
    UPDATE articles_fts SET
        title = new.title,
        author = new.author,
        content_text = new.content_text
        WHERE rowid = new.id;
END;

-- Keep feed_title current in every article row when a feed is renamed.
CREATE TRIGGER feeds_fts_au AFTER UPDATE OF title ON feeds BEGIN
    UPDATE articles_fts SET feed_title = new.title
        WHERE rowid IN (SELECT id FROM articles WHERE feed_id = new.id);
END;
