import type { Words } from "./en";

// Everything the interface says, in Brazilian Portuguese.
export const ptBR: Words = {
  brand: "Convia",
  failure: {
    message: "Algo deu errado.",
    reload: "Recarregar",
  },
  dataProblem: {
    title: {
      newer: "Estes dados são de uma Convia mais nova.",
      damaged: "A Convia não consegue ler os seus dados.",
      "key-unreadable": "A Convia não consegue desbloquear os seus dados.",
      other: "A Convia não consegue abrir os seus dados.",
    },
    explanation: {
      newer:
        "Eles foram gravados por uma versão mais nova da Convia. Atualize a Convia para abri-los; nada foi alterado.",
      damaged: "O arquivo que guarda as suas conversas está danificado.",
      "key-unreadable":
        "A chave deles não abre mais, o que acontece quando a conta do Windows a que pertenciam mudou.",
      other: "Algo impediu a Convia de abrir os seus dados. O log diz o quê.",
    },
    keptAside:
      "Começar do zero guarda os arquivos ilegíveis numa pasta própria; nada é apagado.",
    startOver: "Começar do zero",
  },
};
