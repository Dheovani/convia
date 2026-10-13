import type { Words } from "./en";

// Everything the interface says, in Brazilian Portuguese.
export const ptBR: Words = {
  brand: "Convia",
  failure: {
    message: "Algo deu errado.",
    reload: "Recarregar",
  },
  refusals: {
    name: "Um nome tem de 1 a 32 caracteres.",
    "weak-password": "A senha precisa de pelo menos 12 caracteres.",
    password: "Essa senha não abre este arquivo.",
    "export-file":
      "Este não é uma identidade exportada da Convia, ou está danificado.",
    newer: "Estes dados são de uma Convia mais nova.",
    "no-identity": "Ainda não há uma identidade.",
    unknown: "Algo deu errado.",
  },
  welcome: {
    title: "Boas-vindas à Convia",
    explanation:
      "A Convia cria a sua identidade aqui, neste computador. Ninguém mais a emite nem a guarda.",
    name: "Seu nome",
    nameHint: "O que os seus contatos veem. Não precisa ser único.",
    create: "Criar minha identidade",
    haveOne: "Tenho uma identidade exportada",
  },
  importing: {
    title: "Importar uma identidade",
    chooseFile: "Escolher o arquivo",
    noFile: "Nenhum arquivo escolhido",
    password: "A senha dele",
    import: "Importar",
    replaceWarning:
      "Este computador já tem uma identidade. Importar a guarda, com o histórico, numa pasta própria; nada é apagado.",
    replace: "Guardar a minha e importar",
    cancel: "Cancelar",
  },
  me: {
    name: "Nome",
    rename: "Mudar o nome",
    save: "Salvar",
    publicKey: "Sua chave pública",
    publicKeyHint:
      "É quem você é para os seus contatos. O nome pode mudar; isto não.",
    exportTitle: "Exportar sua identidade",
    exportExplanation:
      "Guarde uma cópia, protegida por senha, para levar a sua identidade a outro computador. Sem ela, perder este computador é perder a sua identidade.",
    password: "Senha",
    confirmPassword: "A mesma senha de novo",
    mismatch: "As duas senhas são diferentes.",
    export: "Exportar",
    exported: "Exportada.",
    deleteTitle: "Apagar sua identidade",
    deleteExplanation:
      "Isto destrói a sua identidade, os seus contatos e as suas conversas neste computador. Os seus contatos não são avisados. Exporte antes, se puder querê-la de volta.",
    deleteConfirm: "Entendo que isto não pode ser desfeito",
    delete: "Apagar minha identidade",
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
    importInstead: "Importar uma identidade exportada",
  },
};
