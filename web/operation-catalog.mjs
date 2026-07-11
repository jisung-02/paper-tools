const response = await fetch("/operation-catalog.json");
if (!response.ok) throw new Error("operation catalog is unavailable");
const catalog = await response.json();

export const operations = Object.freeze(catalog.map((entry) => Object.freeze(entry)));
export const operationsById = new Map(operations.map((entry) => [entry.id, entry]));

export function operation(id) {
  const value = operationsById.get(id);
  if (!value) throw new Error(`unknown operation: ${id}`);
  return value;
}
