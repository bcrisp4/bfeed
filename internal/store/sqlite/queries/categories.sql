-- name: CreateCategory :one
INSERT INTO categories (user_id, title) VALUES (?, ?) RETURNING id;

-- name: GetCategory :one
SELECT * FROM categories WHERE id = ? AND user_id = ?;

-- name: ListCategories :many
SELECT * FROM categories WHERE user_id = ? ORDER BY title COLLATE NOCASE ASC;

-- name: UpdateCategory :execrows
UPDATE categories SET title = ? WHERE id = ? AND user_id = ?;

-- name: DeleteCategory :execrows
DELETE FROM categories WHERE id = ? AND user_id = ?;

-- name: UnreadCountsByCategory :many
SELECT f.category_id AS category_id, COUNT(*) AS n
FROM entries e JOIN feeds f ON e.feed_id = f.id
WHERE e.user_id = ? AND e.status = 'unread'
GROUP BY f.category_id;
