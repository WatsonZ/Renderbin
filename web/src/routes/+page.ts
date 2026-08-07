import { listFiles } from '$lib/api/files';

export async function load() {
	return { files: await listFiles() };
}
