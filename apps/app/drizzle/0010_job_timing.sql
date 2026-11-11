-- When a job's latest attempt started and when the job last finished (done,
-- failed, or an attempt that will be retried). Rows from before this migration
-- keep nulls: their real times are gone. Hand-written, additive only.
ALTER TABLE `jobs` ADD `started_at` integer;
--> statement-breakpoint
ALTER TABLE `jobs` ADD `finished_at` integer;
--> statement-breakpoint
-- The documents list counts current memories per document (memories.ts).
-- Without this, each row in the list scans every memory in the workspace.
CREATE INDEX `memories_source_document_idx` ON `memories` (`source_document_id`);
