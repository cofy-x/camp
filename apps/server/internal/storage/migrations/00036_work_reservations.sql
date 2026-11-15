-- +goose Up
ALTER TABLE work_item_tasks ADD COLUMN reservation_released INTEGER NOT NULL DEFAULT 0 CHECK(reservation_released IN (0,1));

UPDATE work_items
SET value=json_set(value,'$.usage.reservedModelRequests',
  CAST(json_extract(value,'$.usage.reservedModelRequests') AS INTEGER)-COALESCE((
    SELECT SUM(MAX(0,
      CAST(json_extract(t.snapshot,'$.maxModelRequests') AS INTEGER)-
      CAST(json_extract(t.snapshot,'$.modelRequestsUsed') AS INTEGER)))
    FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
    WHERE w.work_item_id=work_items.id AND t.status IN ('completed','cancelled','interrupted')
  ),0)), revision=revision+1
WHERE EXISTS (
  SELECT 1 FROM work_item_tasks w JOIN collaboration_tasks t ON t.id=w.task_id
  WHERE w.work_item_id=work_items.id AND t.status IN ('completed','cancelled','interrupted')
);

UPDATE work_item_tasks SET reservation_released=1
WHERE task_id IN (SELECT id FROM collaboration_tasks WHERE status IN ('completed','cancelled','interrupted'));
