// A file imported with `?raw` is its text, which is how a test reads the theme it checks.
declare module '*?raw' {
  const text: string
  export default text
}
