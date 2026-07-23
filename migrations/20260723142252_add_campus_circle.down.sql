-- module: campus_circle
DELETE FROM comment_pins WHERE target_type = 'campus_circle_post';
DELETE FROM comments WHERE target_type = 'campus_circle_post';
DROP TABLE campus_circle_post_likes;
DROP TABLE campus_circle_post_images;
DROP TABLE campus_circle_posts;
DROP TABLE campus_circle_sections;
