/*
Everything the interface says, in English, which is also the shape every other
language has to fill: a phrase missing from a translation fails to compile.
*/
export const en = {
  brand: "Convia",
  failure: {
    message: "Something went wrong.",
    reload: "Reload",
  },
  refusals: {
    name: "A name has 1 to 32 characters.",
    "weak-password": "The password needs at least 12 characters.",
    password: "That password does not open this file.",
    "export-file": "This is not an exported Convia identity, or it is damaged.",
    newer: "This data belongs to a newer Convia.",
    "no-identity": "There is no identity yet.",
    unknown: "Something went wrong.",
  },
  welcome: {
    title: "Welcome to Convia",
    explanation:
      "Convia makes your identity here, on this computer. Nobody else issues it or holds it.",
    name: "Your name",
    nameHint: "What your contacts see. It does not need to be unique.",
    create: "Create my identity",
    haveOne: "I have an exported identity",
  },
  importing: {
    title: "Import an identity",
    chooseFile: "Choose the file",
    noFile: "No file chosen",
    password: "Its password",
    import: "Import",
    replaceWarning:
      "This computer already has an identity. Importing sets it aside, with its history, in a folder of its own; nothing is deleted.",
    replace: "Set mine aside and import",
    cancel: "Cancel",
  },
  me: {
    name: "Name",
    rename: "Change name",
    save: "Save",
    publicKey: "Your public key",
    publicKeyHint:
      "This is who you are to your contacts. Your name can change; this cannot.",
    exportTitle: "Export your identity",
    exportExplanation:
      "Keep a copy, protected by a password, to bring your identity to another computer. Without it, losing this computer loses your identity.",
    password: "Password",
    confirmPassword: "The same password again",
    mismatch: "The two passwords differ.",
    export: "Export",
    exported: "Exported.",
    deleteTitle: "Delete your identity",
    deleteExplanation:
      "This destroys your identity, your contacts and your conversations on this computer. Your contacts are not told. Export it first if you may want it back.",
    deleteConfirm: "I understand this cannot be undone",
    delete: "Delete my identity",
  },
  dataProblem: {
    title: {
      newer: "This data belongs to a newer Convia.",
      damaged: "Convia cannot read its data.",
      "key-unreadable": "Convia cannot unlock its data.",
      other: "Convia cannot open its data.",
    },
    explanation: {
      newer:
        "It was written by a newer version of Convia. Update Convia to open it; nothing has been changed.",
      damaged: "The file that holds your conversations is damaged.",
      "key-unreadable":
        "Its key no longer opens, which happens when the Windows account it belonged to has changed.",
      other:
        "Something stopped Convia from opening its data. The log says what.",
    },
    keptAside:
      "Starting over keeps the unreadable files in a folder of their own; nothing is deleted.",
    startOver: "Start over",
    importInstead: "Import an exported identity instead",
  },
};

export type Words = typeof en;
