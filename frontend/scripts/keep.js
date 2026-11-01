// vite empties the output folder; keep a placeholder so `go:embed all:dist` always has something to embed.
import { writeFileSync } from 'node:fs';
writeFileSync(new URL('../../backend/internal/web/dist/.gitkeep', import.meta.url), '');
