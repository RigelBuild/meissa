type recovered = import('./m', { with: { 'resolution-mode': 'import' } }).X;
const diagnosed = <T>(x: T) => x;
