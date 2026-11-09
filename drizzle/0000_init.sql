CREATE TABLE `api_keys` (
	`id` text PRIMARY KEY NOT NULL,
	`name` text NOT NULL,
	`key_hash` text NOT NULL,
	`prefix` text NOT NULL,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`last_used_at` integer,
	`revoked_at` integer
);
--> statement-breakpoint
CREATE UNIQUE INDEX `api_keys_key_hash_unique` ON `api_keys` (`key_hash`);--> statement-breakpoint
CREATE TABLE `chunks` (
	`id` text PRIMARY KEY NOT NULL,
	`document_id` text NOT NULL,
	`container` text NOT NULL,
	`idx` integer NOT NULL,
	`text` text NOT NULL,
	FOREIGN KEY (`document_id`) REFERENCES `documents`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE INDEX `chunks_document_idx` ON `chunks` (`document_id`,`idx`);--> statement-breakpoint
CREATE TABLE `documents` (
	`id` text PRIMARY KEY NOT NULL,
	`container` text NOT NULL,
	`source_type` text NOT NULL,
	`title` text,
	`url` text,
	`content` text,
	`status` text DEFAULT 'queued' NOT NULL,
	`error` text,
	`metadata` text,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL
);
--> statement-breakpoint
CREATE INDEX `documents_container_idx` ON `documents` (`container`,`created_at`);--> statement-breakpoint
CREATE TABLE `jobs` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`type` text NOT NULL,
	`payload` text NOT NULL,
	`status` text DEFAULT 'queued' NOT NULL,
	`attempts` integer DEFAULT 0 NOT NULL,
	`run_after` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`last_error` text,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL
);
--> statement-breakpoint
CREATE INDEX `jobs_pending_idx` ON `jobs` (`status`,`run_after`);--> statement-breakpoint
CREATE TABLE `memories` (
	`id` text PRIMARY KEY NOT NULL,
	`container` text NOT NULL,
	`fact` text NOT NULL,
	`keywords` text DEFAULT '' NOT NULL,
	`kind` text DEFAULT 'dynamic' NOT NULL,
	`source_document_id` text,
	`supersedes_id` text,
	`valid_from` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`valid_to` integer,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	FOREIGN KEY (`source_document_id`) REFERENCES `documents`(`id`) ON UPDATE no action ON DELETE set null
);
--> statement-breakpoint
CREATE INDEX `memories_current_idx` ON `memories` (`container`,`valid_to`,`created_at`);--> statement-breakpoint
CREATE INDEX `memories_supersedes_idx` ON `memories` (`supersedes_id`);--> statement-breakpoint
CREATE TABLE `profiles` (
	`container` text PRIMARY KEY NOT NULL,
	`static_text` text DEFAULT '' NOT NULL,
	`dynamic_text` text DEFAULT '' NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL
);
