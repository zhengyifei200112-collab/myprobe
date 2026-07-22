ALTER TABLE nodes ADD COLUMN custom_badges_json TEXT NOT NULL DEFAULT '[]';
ALTER TABLE nodes ADD COLUMN custom_links_json TEXT NOT NULL DEFAULT '[]';
