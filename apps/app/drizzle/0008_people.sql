-- People mentioned in facts, per workspace and container. Hand-written: 0006
-- and 0007 land on other branches. See apps/site/content/docs/people.mdx.
--
-- aliases, emails and handles are JSON arrays of strings. `manual` is 1 once a
-- person was edited in the dashboard: extraction then stops rewriting the
-- name, role, organization and summary, and only adds aliases, contacts and links.
CREATE TABLE `people` (
	`id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`container` text NOT NULL,
	`name` text NOT NULL,
	`aliases` text DEFAULT '[]' NOT NULL,
	`role` text,
	`organization` text,
	`emails` text DEFAULT '[]' NOT NULL,
	`handles` text DEFAULT '[]' NOT NULL,
	`summary` text DEFAULT '' NOT NULL,
	`manual` integer DEFAULT 0 NOT NULL,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL
);
--> statement-breakpoint
CREATE INDEX `people_scope_idx` ON `people` (`workspace_id`, `container`);
--> statement-breakpoint
-- Which facts mention which person. A superseding version inherits the links
-- of the version it replaces; the old version keeps its own.
CREATE TABLE `person_memories` (
	`person_id` text NOT NULL,
	`memory_id` text NOT NULL,
	`workspace_id` text NOT NULL,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	PRIMARY KEY (`person_id`, `memory_id`),
	FOREIGN KEY (`person_id`) REFERENCES `people`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`memory_id`) REFERENCES `memories`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `person_memories_memory_idx` ON `person_memories` (`memory_id`);
--> statement-breakpoint
-- Facts already checked for people (by extraction or the backfill), so the
-- backfill never sends the same fact to the model twice.
CREATE TABLE `people_scans` (
	`memory_id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`scanned_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	FOREIGN KEY (`memory_id`) REFERENCES `memories`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE TRIGGER `people_workspace_required` BEFORE INSERT ON `people` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'people.workspace_id is required'); END;
--> statement-breakpoint
CREATE TRIGGER `person_memories_workspace_required` BEFORE INSERT ON `person_memories` WHEN NEW.`workspace_id` IS NULL OR NEW.`workspace_id` = ''
BEGIN SELECT RAISE(ABORT, 'person_memories.workspace_id is required'); END;
