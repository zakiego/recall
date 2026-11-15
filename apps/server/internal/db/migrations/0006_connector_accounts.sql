-- Connected third-party accounts (Gmail for now). One per user, workspace and
-- provider. `tokens` is AES-256-GCM ciphertext (see src/server/connectors/crypto.ts),
-- never plain OAuth tokens.
CREATE TABLE `connector_accounts` (
	`id` text PRIMARY KEY NOT NULL,
	`workspace_id` text NOT NULL,
	`user_id` text NOT NULL,
	`provider` text NOT NULL,
	`account_email` text,
	`scope` text NOT NULL,
	`tokens` text NOT NULL,
	`status` text DEFAULT 'active' NOT NULL,
	`created_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`updated_at` integer DEFAULT (cast(unixepoch('subsec') * 1000 as integer)) NOT NULL,
	`last_used_at` integer,
	FOREIGN KEY (`user_id`) REFERENCES `user`(`id`) ON UPDATE no action ON DELETE cascade
);
--> statement-breakpoint
CREATE UNIQUE INDEX `connector_accounts_owner_unique` ON `connector_accounts` (`workspace_id`, `user_id`, `provider`);
--> statement-breakpoint
-- Leaving or being removed from a workspace drops your connections there.
CREATE TRIGGER `connector_accounts_member_removed` AFTER DELETE ON `member`
BEGIN
	DELETE FROM `connector_accounts` WHERE `workspace_id` = OLD.`organization_id` AND `user_id` = OLD.`user_id`;
END;
