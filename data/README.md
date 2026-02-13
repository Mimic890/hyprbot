# Runtime Data

This directory stores local runtime state for docker-compose:

- `data/postgres` - PostgreSQL data files
- `data/redis` - Redis AOF/RDB files
- `data/bot` - bot local files (for future sqlite/dev artifacts, temp files, etc.)

Safe cleanup for full reset (destructive):

```bash
docker compose down
rm -rf data/postgres/* data/redis/* data/bot/*
```
