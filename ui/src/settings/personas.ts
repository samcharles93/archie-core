/** The personas resource document. */
export interface PersonaCollection {
  default: string;
  personas: { name: string; prompt: string }[];
}

// The collection must always name a default that exists (the server refuses
// one that does not), so every edit that touches a name keeps it valid.

export function renamePersona(c: PersonaCollection, index: number, name: string): void {
  const persona = c.personas[index];
  if (!persona) return;
  if (c.default === persona.name) c.default = name;
  persona.name = name;
}

/** removePersona deletes one persona; the last one cannot go. */
export function removePersona(c: PersonaCollection, index: number): boolean {
  if (c.personas.length <= 1 || !c.personas[index]) return false;
  const [removed] = c.personas.splice(index, 1);
  if (removed!.name === c.default) c.default = c.personas[0]!.name;
  return true;
}

/** duplicatePersona copies one persona under a free name and returns its index. */
export function duplicatePersona(c: PersonaCollection, index: number): number {
  const source = c.personas[index];
  if (!source) return -1;
  const taken = new Set(c.personas.map((p) => p.name));
  let name = `${source.name}-copy`;
  for (let n = 2; taken.has(name); n++) name = `${source.name}-copy-${n}`;
  c.personas.splice(index + 1, 0, { name, prompt: source.prompt });
  return index + 1;
}
