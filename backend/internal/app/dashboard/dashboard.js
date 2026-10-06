"use strict";

const feedback = document.getElementById("copy-feedback");
function announce(message) {
  if (feedback) feedback.textContent = message;
}

document.querySelectorAll("[data-copy]").forEach((button) => {
  button.addEventListener("click", async () => {
    const input = document.getElementById(button.dataset.copy);
    if (!input) return;
    try {
      await navigator.clipboard.writeText(input.value);
      const oldTitle = button.title;
      button.title = "Copied";
      button.setAttribute("aria-label", "Copied");
      announce(oldTitle.replace("Copy", "Copied"));
      window.setTimeout(() => {
        button.title = oldTitle;
        button.setAttribute("aria-label", oldTitle);
      }, 1200);
    } catch {
      // Clipboard access may be unavailable on HTTP homelab deployments.
      // Select the real value, including a masked key, for manual copying.
      input.type = "text";
      input.focus();
      input.select();
      const toggle = document.querySelector("[data-toggle-key]");
      if (input.id === "api-key" && toggle) {
        toggle.setAttribute("aria-pressed", "true");
        toggle.setAttribute("aria-label", "Hide API key");
        toggle.title = "Hide API key";
      }
      announce(
        "Clipboard unavailable. The value is selected; copy it using your keyboard or the selection menu.",
      );
    }
  });
});

document
  .querySelector("[data-toggle-key]")
  ?.addEventListener("click", (event) => {
    const input = document.getElementById("api-key");
    if (!input) return;
    const visible = input.type === "password";
    input.type = visible ? "text" : "password";
    const button = event.currentTarget;
    button.setAttribute("aria-pressed", String(visible));
    button.setAttribute(
      "aria-label",
      visible ? "Hide API key" : "Show API key",
    );
    button.title = visible ? "Hide API key" : "Show API key";
  });

// Keep normal POST forms; request confirmation before invalidating clients' keys.
document
  .querySelector("[data-confirm-rotate]")
  ?.addEventListener("submit", (event) => {
    if (
      !window.confirm(
        "Generate a new API key? Apps using the current key will stop connecting until you update them.",
      )
    ) {
      event.preventDefault();
    }
  });

document.querySelectorAll("[data-close-message]").forEach((button) => {
  button.addEventListener("click", () => button.closest(".message")?.remove());
});

const deleteDialog = document.getElementById("delete-dialog");
const vaultInput = document.getElementById("delete-vault-id");
const deleteDialogText = document.getElementById("delete-dialog-text");
document.querySelectorAll("[data-delete-vault]").forEach((button) => {
  button.addEventListener("click", () => {
    if (!deleteDialog || !vaultInput || !deleteDialogText) return;
    vaultInput.value = button.dataset.deleteVault;
    deleteDialogText.textContent =
      'Delete "' +
      button.dataset.deleteName +
      '"? This vault will be hidden and blocked from future sync.';
    deleteDialog.showModal();
  });
});
document.querySelectorAll("[data-close-delete]").forEach((button) => {
  button.addEventListener("click", () => deleteDialog && deleteDialog.close());
});

const restoreDialog = document.getElementById("restore-vaults-dialog");
document.querySelectorAll("[data-open-restore-vaults]").forEach((button) => {
  button.addEventListener(
    "click",
    () => restoreDialog && restoreDialog.showModal(),
  );
});
document.querySelectorAll("[data-close-restore-vaults]").forEach((button) => {
  button.addEventListener(
    "click",
    () => restoreDialog && restoreDialog.close(),
  );
});

const purgeDialog = document.getElementById("purge-vault-dialog");
const purgeVaultInput = document.getElementById("purge-vault-id");
const purgeVaultText = document.getElementById("purge-vault-text");
document.querySelectorAll("[data-purge-vault]").forEach((button) => {
  button.addEventListener("click", () => {
    if (!purgeDialog || !purgeVaultInput || !purgeVaultText) return;
    purgeVaultInput.value = button.dataset.purgeVault;
    purgeVaultText.textContent =
      'Permanently delete "' +
      button.dataset.purgeName +
      '"? This cannot be restored.';
    purgeDialog.showModal();
  });
});
document.querySelectorAll("[data-close-purge-vault]").forEach((button) => {
  button.addEventListener("click", () => purgeDialog && purgeDialog.close());
});

const deleteUserDialog = document.getElementById("delete-user-dialog");
const deleteUserInput = document.getElementById("delete-user-id");
const deleteUserText = document.getElementById("delete-user-text");
document.querySelectorAll("[data-delete-user]").forEach((button) => {
  button.addEventListener("click", () => {
    if (
      !deleteUserDialog ||
      !deleteUserInput ||
      !deleteUserText ||
      button.disabled
    )
      return;
    deleteUserInput.value = button.dataset.deleteUser;
    deleteUserText.textContent =
      'Delete "' +
      button.dataset.deleteUserEmail +
      '" and all owned sync data? This cannot be undone.';
    deleteUserDialog.showModal();
  });
});
document.querySelectorAll("[data-close-delete-user]").forEach((button) => {
  button.addEventListener(
    "click",
    () => deleteUserDialog && deleteUserDialog.close(),
  );
});
