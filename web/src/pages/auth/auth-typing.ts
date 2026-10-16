export function permitsAuthTyping(type: string, autocomplete: string, passwordWrapper: boolean) {
    return ["text", "email", "tel", "url", "search"].includes(type) && !autocomplete.includes("password") && !passwordWrapper;
}

export function insertedAuthText(previous: string, next: string) {
    const oldCharacters = Array.from(previous);
    const characters = Array.from(next);
    let start = 0;
    while (start < oldCharacters.length && start < characters.length && oldCharacters[start] === characters[start]) start++;
    let end = 0;
    while (end < oldCharacters.length - start && end < characters.length - start && oldCharacters[oldCharacters.length - end - 1] === characters[characters.length - end - 1]) end++;
    const inserted = characters.slice(start, characters.length - end).join("");
    return inserted ? { before: characters.slice(0, start).join(""), inserted, after: characters.slice(characters.length - end).join("") } : null;
}
