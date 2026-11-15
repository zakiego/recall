-- Pull connectors for Zaki's own apps (Oslo, Finansial). Hand-written, additive only.
--
-- One row per workspace and provider. Recall pulls the app's changes feed on a
-- schedule; the app never calls Recall. The API key is sealed with
-- CONNECTOR_SECRET (connectors/crypto.ts), bound to the row id and workspace.
-- `cursor` is the feed's opaque cursor and only moves after a page is stored.
CREATE TABLE `app_connectors` (
	`id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`provider` text NOT NULL,
	`container` text NOT NULL,
	`base_url` text NOT NULL,
	`api_key` text,
	`enabled` integer DEFAULT 0 NOT NULL,
	`cursor` text,
	`caught_up_at` integer,
	`last_sync_at` integer,
	`last_success_at` integer,
	`last_error` text,
	`settings` text DEFAULT '{}' NOT NULL,
	`created_by` text,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	FOREIGN KEY (`workspace_id`) REFERENCES `organization`(`id`) ON UPDATE no action ON DELETE cascade,
	FOREIGN KEY (`created_by`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE UNIQUE INDEX `app_connectors_workspace_provider_unique` ON `app_connectors` (`workspace_id`, `provider`);
--> statement-breakpoint

-- Oslo events waiting for their local day to end. A day's digest is written
-- once, after the day is over, so it never has to change. `item_key` dedupes
-- an event (a task completed twice in one day is one line). Rows for a written
-- day are deleted, except `project-created:` keys, which remember that a
-- project was already reported.
CREATE TABLE `app_connector_staging` (
	`connector_id` text NOT NULL,
	`day` text NOT NULL,
	`item_key` text NOT NULL,
	`payload` text NOT NULL,
	`written_at` integer,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	PRIMARY KEY (`connector_id`, `item_key`),
	FOREIGN KEY (`connector_id`) REFERENCES `app_connectors`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `app_connector_staging_day_idx` ON `app_connector_staging` (`connector_id`, `day`);
