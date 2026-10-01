document.addEventListener("click", async (event) => {
  const button = event.target.closest("button[data-copy-target]");
  if (!button) return;
  const field = document.getElementById(button.dataset.copyTarget);
  if (!field) return;
  field.focus();
  field.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } catch { /* The selected text remains available for manual copy. */ }
  if (!copied && navigator.clipboard?.writeText) {
    try {
      await Promise.race([
        navigator.clipboard.writeText(field.value).then(() => { copied = true; }),
        new Promise((_, reject) => window.setTimeout(reject, 1000)),
      ]);
    } catch { /* The selected text remains available for manual copy. */ }
  }
  button.textContent = copied ? "Скопировано" : "Текст выделен — нажмите ⌘C";
  if (copied) window.setTimeout(() => { button.textContent = "Скопировать промпт"; }, 2500);
});
