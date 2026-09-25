const tasks = (n: number) => (n === 1 ? "1 task" : `${n} tasks`);

/** headline states what is running and what needs a human, for the header. */
export function headline(running: number, attention: number): { running: string; attention: string } {
  return {
    running: running ? `${tasks(running)} running` : "Nothing is running",
    attention: attention ? `${tasks(attention)} need${attention === 1 ? "s" : ""} you` : "Nothing needs you",
  };
}
