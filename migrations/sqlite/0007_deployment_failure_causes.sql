-- +goose Up
UPDATE outbox_events
SET payload = json_remove(
    json_set(payload, '$.causes', json_array(json_object(
        'code', json_extract(payload, '$.fault_code'),
        'summary', coalesce(json_extract(payload, '$.fault_summary'), ''),
        'temporary', json(CASE json_type(payload, '$.temporary') WHEN 'true' THEN 'true' ELSE 'false' END)
    ))),
    '$.fault_code', '$.fault_summary', '$.temporary'
)
WHERE event_type IN ('DeployFailed', 'DeployInterrupted', 'DeployPreparationFailed')
  AND schema_version = 1
  AND json_type(payload, '$.causes') IS NULL
  AND json_type(payload, '$.fault_code') = 'text'
  AND json_extract(payload, '$.fault_code') <> '';

-- +goose Down
-- Forward-only: the previous format cannot represent multiple causes.
SELECT * FROM deployment_failure_causes_migration_0007_is_forward_only;
