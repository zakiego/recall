-- A note set when the extractor finds new content that disagrees with a memory
-- and can't tell which one is right. Both sides get it; resolving clears it.
ALTER TABLE `memories` ADD `review_note` text;
