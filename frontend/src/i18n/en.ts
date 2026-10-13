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
  },
};

export type Words = typeof en;
