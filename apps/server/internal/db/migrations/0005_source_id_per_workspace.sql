-- 0003 made source_id unique per container. Container names repeat across
-- workspaces, so the same source must be allowed once per workspace too.
DROP INDEX `documents_container_source_id_unique`;
--> statement-breakpoint
CREATE UNIQUE INDEX `documents_workspace_source_id_unique` ON `documents` (`workspace_id`, `container`, `source_id`) WHERE "documents"."source_id" is not null;
