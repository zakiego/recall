-- Optional caller-supplied id (e.g. gmail:1a1ab7843db48820) so the same source
-- is ingested once per container. Hand-written: 0002 lands on another branch.
ALTER TABLE `documents` ADD `source_id` text;
--> statement-breakpoint
CREATE UNIQUE INDEX `documents_container_source_id_unique` ON `documents` (`container`,`source_id`) WHERE "documents"."source_id" is not null;
