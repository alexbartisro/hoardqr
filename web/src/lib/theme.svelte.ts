// Theme preference: auto (follow the OS) / light / dark. The resolved theme
// is a `.dark` class on <html> — layout.css keys every dark variable and
// `dark:` utility off it. app.html's inline script sets the same class
// before first paint (no flash of the wrong theme), so this module only has
// to keep it in sync afterwards; the two must agree on the storage key and
// on how "auto" resolves.
export type ThemeMode = 'auto' | 'light' | 'dark';

const STORAGE_KEY = 'theme';
const ORDER: ThemeMode[] = ['auto', 'light', 'dark'];

function readStored(): ThemeMode {
	try {
		const v = localStorage.getItem(STORAGE_KEY);
		return v === 'light' || v === 'dark' ? v : 'auto';
	} catch {
		// Storage blocked (private mode, etc.) — behave as "auto", unpersisted.
		return 'auto';
	}
}

function apply(mode: ThemeMode) {
	const dark = mode === 'dark' || (mode === 'auto' && matchMedia('(prefers-color-scheme: dark)').matches);
	document.documentElement.classList.toggle('dark', dark);
	document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#0a0a0a' : '#ffffff');
}

class Theme {
	mode = $state<ThemeMode>('auto');

	/** Call once from the root layout's onMount. Returns a cleanup function. */
	init(): () => void {
		this.mode = readStored();
		apply(this.mode);
		// "Auto" must track the OS live (e.g. macOS switching at sunset).
		const mq = matchMedia('(prefers-color-scheme: dark)');
		const onChange = () => this.mode === 'auto' && apply('auto');
		mq.addEventListener('change', onChange);
		return () => mq.removeEventListener('change', onChange);
	}

	set(mode: ThemeMode) {
		this.mode = mode;
		try {
			if (mode === 'auto') localStorage.removeItem(STORAGE_KEY);
			else localStorage.setItem(STORAGE_KEY, mode);
		} catch {
			// Unpersisted for this session only.
		}
		apply(mode);
	}

	/** auto → light → dark → auto. */
	cycle() {
		this.set(ORDER[(ORDER.indexOf(this.mode) + 1) % ORDER.length]);
	}
}

export const theme = new Theme();
