export function applyTheme(dark: boolean) {
 document.documentElement.classList.toggle('dark', dark)
 localStorage.setItem('nekopass-theme-v3', dark ? 'dark' : 'light')
 document.querySelector('meta[name="theme-color"]')?.setAttribute('content', dark ? '#080808' : '#ffffff')
}
