// Registers the full Lucide icon set with @iconify/svelte's in-memory store
// so <Icon icon="lucide:..."> resolves offline. Without this, the default
// Icon component falls back to fetching from the public Iconify API at
// runtime — unacceptable for a self-hosted app. Side-effect import only,
// pulled in once from the root layout.
import { addCollection } from '@iconify/svelte';
import lucide from '@iconify-json/lucide/icons.json';

addCollection(lucide);
