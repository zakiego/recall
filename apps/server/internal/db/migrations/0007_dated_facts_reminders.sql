-- Dated facts and email reminders. Hand-written, additive only, so it applies
-- before or after 0006 and 0008 (other branches) in any order.
--
-- A date is stored as a full ISO date (YYYY-MM-DD) plus its precision. A month
-- or year date is stored as the first day of that month or year: "2027-12" is
-- 2027-12-01 with precision 'month'. Reminders count from that day, so they
-- fire early rather than late.
ALTER TABLE `memories` ADD `event_date` text;
--> statement-breakpoint
ALTER TABLE `memories` ADD `event_precision` text;
--> statement-breakpoint
ALTER TABLE `memories` ADD `due_date` text;
--> statement-breakpoint
ALTER TABLE `memories` ADD `due_precision` text;
--> statement-breakpoint
CREATE INDEX `memories_due_idx` ON `memories` (`workspace_id`, `due_date`) WHERE `valid_to` IS NULL AND `due_date` IS NOT NULL;
--> statement-breakpoint

-- One row per user. Reminders cover every workspace the user is a member of.
-- The email is its own column: Better Auth users here carry a placeholder email.
CREATE TABLE `reminder_settings` (
	`user_id` text PRIMARY KEY NOT NULL,
	`enabled` integer DEFAULT 0 NOT NULL,
	`email` text,
	`lead_days` text DEFAULT '[30,1]' NOT NULL,
	`timezone` text DEFAULT 'UTC' NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint

-- Each reminder (user, fact, due date, lead time) is sent once. A row is
-- written as 'pending' before the email goes out and becomes 'sent' after,
-- so a failed send is retried on the next run and a sent one never repeats.
CREATE TABLE `reminders_sent` (
	`id` integer PRIMARY KEY AUTOINCREMENT NOT NULL,
	`user_id` text NOT NULL,
	`workspace_id` text NOT NULL,
	`memory_id` text NOT NULL,
	`due_date` text NOT NULL,
	`lead_days` integer NOT NULL,
	`status` text DEFAULT 'pending' NOT NULL,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`sent_at` integer,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `reminders_sent_unique` ON `reminders_sent` (`user_id`, `memory_id`, `due_date`, `lead_days`);
--> statement-breakpoint
CREATE INDEX `reminders_sent_memory_idx` ON `reminders_sent` (`memory_id`);
