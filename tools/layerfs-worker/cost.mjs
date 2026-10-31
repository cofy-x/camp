import { readFile } from 'node:fs/promises';
import process from 'node:process';
import { fileURLToPath } from 'node:url';

export async function cgroupCost() {
  const cpu = Object.fromEntries(
    (await readFile('/sys/fs/cgroup/cpu.stat', 'utf8'))
      .trim()
      .split('\n')
      .map((line) => {
        const [name, value] = line.split(/\s+/);
        return [name, Number(value)];
      }),
  );
  const memory = {};
  for (const field of ['current', 'peak'])
    memory[field] = Number(
      await readFile(`/sys/fs/cgroup/memory.${field}`, 'utf8'),
    );
  return {
    cpu,
    memoryBytes: memory,
    scope:
      'Linux cgroup v2 lifetime CPU and memory, including child processes and charged file cache; not additive with VM allocation or RSS',
  };
}

if (process.argv[1] === fileURLToPath(import.meta.url))
  process.stdout.write(JSON.stringify(await cgroupCost()));
