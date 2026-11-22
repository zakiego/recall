-- The Oslo, Finansial and WhatsApp adapters are gone: every feed sends the
-- generic kinds now, and only event lines ("ev:" keys) are staged. Rows the
-- old adapters staged (task:, project-created:, wa:, ...) would never be
-- written or cleared, so drop them. Documents they wrote stay.
DELETE FROM `app_connector_staging` WHERE substr(`item_key`, 1, 3) <> 'ev:';
