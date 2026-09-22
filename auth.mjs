const key = process.env.OPENROUTER_API_KEY;
if (!key?.trim()) {
  process.stderr.write('OPENROUTER_API_KEY is not set.\n');
  process.exitCode = 1;
} else {
  process.stdout.write(key.trim());
}
