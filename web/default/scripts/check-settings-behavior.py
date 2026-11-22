# Run via stdin from web/default; no emitted files, network, or project build.
# Python only coordinates serial esbuild transforms and Node behavior tests.
import glob
import json
import os
import pathlib
import signal
import subprocess
import threading
import time

web = pathlib.Path.cwd().parent
compiler = glob.glob(str(web / 'node_modules/.bun/esbuild@*/node_modules/esbuild/bin/esbuild'))[0]
sources = [
    'default/src/features/playground/hooks/use-account-session.ts',
    'classic/src/hooks/playground/accountSession.js',
    'default/src/features/system-settings/operations/use-script-plugin-request.ts',
    'classic/src/pages/Setting/Operation/useScriptPluginRequests.js',
    'default/src/lib/api.ts',
    'default/src/features/system-settings/operations/script-plugins-api.ts',
    'default/src/features/system-settings/operations/script-plugins-panel.tsx',
    'default/src/features/system-settings/operations/script-plugin-bindings.tsx',
    'classic/src/pages/Setting/Operation/SettingsScriptPlugins.jsx',
    'default/src/features/system-settings/models/reset-model-ratios.ts',
    'default/src/features/system-settings/models/ratio-settings-card.tsx',
]
peak = 0


def rss_tree(pid):
    try:
        with open(f'/proc/{pid}/statm') as handle:
            rss = int(handle.read().split()[1]) * os.sysconf('SC_PAGE_SIZE')
        with open(f'/proc/{pid}/task/{pid}/children') as handle:
            children = list(map(int, handle.read().split()))
        return rss + sum(rss_tree(child) for child in children)
    except (OSError, IndexError):
        return 0


def run(args, data):
    process = subprocess.Popen(
        args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
        start_new_session=True, env={**os.environ, 'GOMAXPROCS': '1', 'GOMEMLIMIT': '24MiB'},
    )
    done = threading.Event()

    def monitor():
        global peak
        start = time.monotonic()
        while not done.wait(.005):
            peak = max(peak, rss_tree(os.getpid()))
            if peak > 80 * 1048576 or time.monotonic() - start > 25:
                try:
                    os.killpg(process.pid, signal.SIGKILL)
                except ProcessLookupError:
                    pass
                return

    thread = threading.Thread(target=monitor)
    thread.start()
    out, err = process.communicate(data.encode())
    done.set()
    thread.join()
    if err:
        print(err.decode(), end='')
    if process.returncode:
        print(out.decode(), end='')
        raise SystemExit(f'Check stopped/failed exit={process.returncode}; runner+children peak RSS={peak / 1048576:.1f} MiB')
    return out.decode()


compiled = {}
for path in sources:
    loader = 'tsx' if path.endswith('.tsx') else 'ts' if path.endswith('.ts') else 'jsx'
    compiled[str(web / path)] = run(
        [compiler, f'--loader={loader}', '--format=cjs', '--jsx=automatic', '--log-level=error'],
        (web / path).read_text(),
    )
script = 'globalThis.__frontendTestSource = ' + json.dumps(compiled) + "; await import('./scripts/script-session.test.mjs'); await import('./scripts/reset-model-ratios.test.mjs');"
for iteration in range(1, 6):
    print(f'Behavior pass {iteration}/5', flush=True)
    print(run(['node', '--max-old-space-size=24', '--input-type=module'], script), end='')
print(f'11 source syntax checks and 5 behavior passes completed; runner+children peak RSS={peak / 1048576:.1f} MiB')
