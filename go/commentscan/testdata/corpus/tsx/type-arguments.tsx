// Extracted from tree-sitter-typescript test/corpus/expressions.txt, case
// "Type arguments in JSX".
const element = <Element<T>>hi</Element>;
const selfClosing = <Element<T> />;
const fragment = <>fragment</>;
