-- Links the two sides of a conflict. Before this, the other fact was only
-- quoted in review_note, so resolving or deleting one side left the other
-- flagged. Rows flagged before this migration keep a null link.
ALTER TABLE `memories` ADD `conflicts_with_id` text;
--> statement-breakpoint
CREATE INDEX `memories_conflicts_with_idx` ON `memories` (`conflicts_with_id`);
